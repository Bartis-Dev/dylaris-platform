package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	pb "dylaris-proto/node"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// CoreInfo is the heartbeat payload written by each Core instance to Redis.
type CoreInfo struct {
	ID       string `json:"id"`
	GRPCAddr string `json:"grpc_addr"`
	IPs      struct {
		Public  string   `json:"public"`
		Private []string `json:"private"`
	} `json:"ips"`
}

// coreConnection holds the gRPC client connection and stream to one Core.
type coreConnection struct {
	conn    *grpc.ClientConn
	stream  pb.NodeService_NodeConnectClient
	cancel  context.CancelFunc
	handler *StreamHandler
}

// pendingWrite tracks an in-flight file write operation (WriteReq → Chunks → TransferDone).
// Chunks are written directly to a temp file on disk (zero RAM buffering).
type pendingWrite struct {
	serverUUID string
	path       string
	tempFile   *os.File
	tempPath   string
	lastActive time.Time
}

// MeshManager discovers Core instances via Redis and maintains outbound
// gRPC connections to each one. This creates the full-mesh topology.
type MeshManager struct {
	nodeToken string
	rdb       *redis.Client
	handler   *StreamHandler

	connections   map[string]*coreConnection // coreID → connection
	mu            sync.Mutex
	pendingWrites map[string]*pendingWrite // requestID → write buffer
	writeMu       sync.Mutex
}

func NewMeshManager(nodeToken string, rdb *redis.Client, handler *StreamHandler) *MeshManager {
	return &MeshManager{
		nodeToken:     nodeToken,
		rdb:           rdb,
		handler:       handler,
		connections:   make(map[string]*coreConnection),
		pendingWrites: make(map[string]*pendingWrite),
	}
}

// Run starts the discovery loop. Scans Redis every 10s for active Cores.
func (m *MeshManager) Run(ctx context.Context) {
	log.Println("gRPC Mesh: Starting Core discovery loop")

	// Immediate first scan
	m.scanCores(ctx)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	cleanupTicker := time.NewTicker(60 * time.Second)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.closeAll()
			return
		case <-ticker.C:
			m.scanCores(ctx)
		case <-cleanupTicker.C:
			m.cleanupStalePendingWrites()
		}
	}
}

// cleanupStalePendingWrites removes pending writes that have been inactive for > 5 minutes.
// This prevents memory/disk leaks from aborted uploads.
func (m *MeshManager) cleanupStalePendingWrites() {
	threshold := time.Now().Add(-5 * time.Minute)
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	for reqID, pw := range m.pendingWrites {
		if pw.lastActive.Before(threshold) {
			log.Printf("gRPC Mesh: Cleaning up stale upload (request_id=%s, path=%s)", reqID, pw.path)
			pw.tempFile.Close()
			os.Remove(pw.tempPath)
			delete(m.pendingWrites, reqID)
		}
	}
}

func (m *MeshManager) scanCores(ctx context.Context) {
	keys, err := m.rdb.Keys(ctx, "dylaris:core:*").Result()
	if err != nil {
		log.Printf("gRPC Mesh: Redis scan error: %v", err)
		return
	}

	activeCores := make(map[string]bool)

	for _, key := range keys {
		val, err := m.rdb.Get(ctx, key).Result()
		if err != nil {
			continue
		}

		var info CoreInfo
		if err := json.Unmarshal([]byte(val), &info); err != nil {
			continue
		}

		activeCores[info.ID] = true

		m.mu.Lock()
		_, exists := m.connections[info.ID]
		m.mu.Unlock()

		if !exists {
			go m.connectToCore(ctx, info)
		}
	}

	// Remove connections to Cores that are no longer active
	m.mu.Lock()
	for coreID, conn := range m.connections {
		if !activeCores[coreID] {
			log.Printf("gRPC Mesh: Core %s disappeared, disconnecting", coreID)
			conn.cancel()
			conn.conn.Close()
			delete(m.connections, coreID)
		}
	}
	m.mu.Unlock()
}

func (m *MeshManager) connectToCore(parentCtx context.Context, info CoreInfo) {
	// IP Pairing: try private IPs first (500ms timeout), fallback to advertised gRPC addr
	targetAddr := m.resolveAddr(info)

	log.Printf("gRPC Mesh: Connecting to Core %s at %s", info.ID, targetAddr)

	conn, err := grpc.NewClient(targetAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                20 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		log.Printf("gRPC Mesh: Failed to connect to Core %s: %v", info.ID, err)
		return
	}

	ctx, cancel := context.WithCancel(parentCtx)

	client := pb.NewNodeServiceClient(conn)
	stream, err := client.NodeConnect(ctx)
	if err != nil {
		log.Printf("gRPC Mesh: Failed to open stream to Core %s: %v", info.ID, err)
		cancel()
		conn.Close()
		return
	}

	// Step 1: Send auth
	nodeIPs := getNodeIPs()
	if err := stream.Send(&pb.NodeMessage{
		Payload: &pb.NodeMessage_Auth{
			Auth: &pb.NodeAuth{
				NodeToken: m.nodeToken,
				Ips: &pb.NodeIPs{
					Public:  nodeIPs.Public,
					Private: nodeIPs.Private,
				},
			},
		},
	}); err != nil {
		log.Printf("gRPC Mesh: Failed to send auth to Core %s: %v", info.ID, err)
		cancel()
		conn.Close()
		return
	}

	// Step 2: Wait for auth result
	authResp, err := stream.Recv()
	if err != nil {
		log.Printf("gRPC Mesh: Failed to receive auth result from Core %s: %v", info.ID, err)
		cancel()
		conn.Close()
		return
	}

	authResult := authResp.GetAuthResult()
	if authResult == nil || !authResult.Ok {
		msg := "unknown"
		if authResult != nil {
			msg = authResult.Message
		}
		log.Printf("gRPC Mesh: Auth rejected by Core %s: %s", info.ID, msg)
		cancel()
		conn.Close()
		return
	}

	log.Printf("gRPC Mesh: Connected to Core %s ✓", info.ID)

	// Register connection
	cc := &coreConnection{conn: conn, stream: stream, cancel: cancel, handler: m.handler}
	m.mu.Lock()
	m.connections[info.ID] = cc
	m.mu.Unlock()

	// Step 3: Read loop — handle incoming requests from Core
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("gRPC Mesh: Stream error from Core %s: %v", info.ID, err)
			}
			break
		}

		// Sequential to preserve message ordering (WriteReq → Chunks → TransferDone)
		m.handleRequest(stream, msg)
	}

	// Cleanup
	m.mu.Lock()
	delete(m.connections, info.ID)
	m.mu.Unlock()
	cancel()
	conn.Close()
	log.Printf("gRPC Mesh: Disconnected from Core %s", info.ID)
}

func (m *MeshManager) handleRequest(stream pb.NodeService_NodeConnectClient, msg *pb.NodeMessage) {
	// Handle write data chunks — write directly to temp file on disk
	if chunk := msg.GetChunk(); chunk != nil {
		m.writeMu.Lock()
		if pw, ok := m.pendingWrites[msg.RequestId]; ok {
			pw.lastActive = time.Now()
			if _, err := pw.tempFile.WriteAt(chunk.Data, chunk.Offset); err != nil {
				log.Printf("gRPC Mesh: Write chunk to disk failed (request_id=%s): %v", msg.RequestId, err)
				// Check for EDQUOT (disk quota exceeded)
				if errors.Is(err, syscall.EDQUOT) {
					// Clean up temp file and remove pending write
					pw.tempFile.Close()
					os.Remove(pw.tempPath)
					delete(m.pendingWrites, msg.RequestId)
					m.writeMu.Unlock()
					// Send error response to Core
					stream.Send(errorMsg(msg.RequestId, 413, "Speicherlimit erreicht"))
					return
				}
			}
		}
		m.writeMu.Unlock()
		return
	}

	// Handle transfer complete — close temp file, move to final path
	if done := msg.GetTransferDone(); done != nil {
		m.writeMu.Lock()
		pw, ok := m.pendingWrites[msg.RequestId]
		delete(m.pendingWrites, msg.RequestId)
		m.writeMu.Unlock()
		if ok {
			pw.tempFile.Close()
			// Truncate to exact size (file may be sparse from WriteAt)
			if done.TotalBytes > 0 {
				os.Truncate(pw.tempPath, done.TotalBytes)
			}
			finalPath, err := m.handler.resolveFinalPath(pw.serverUUID, pw.path)
			if err != nil {
				log.Printf("gRPC Mesh: Resolve path failed (request_id=%s): %v", msg.RequestId, err)
				os.Remove(pw.tempPath)
				stream.Send(errorMsg(msg.RequestId, 500, err.Error()))
			} else if err := os.Rename(pw.tempPath, finalPath); err != nil {
				log.Printf("gRPC Mesh: Move file failed (request_id=%s): %v", msg.RequestId, err)
				os.Remove(pw.tempPath)
				if errors.Is(err, syscall.EDQUOT) {
					stream.Send(errorMsg(msg.RequestId, 413, "Speicherlimit erreicht"))
				} else {
					stream.Send(errorMsg(msg.RequestId, 500, err.Error()))
				}
			} else {
				stream.Send(&pb.NodeMessage{
					RequestId: msg.RequestId,
					Payload:   &pb.NodeMessage_Result{Result: &pb.OpResult{Message: "written"}},
				})
			}
		}
		return
	}

	// If this is a WriteReq, create temp file and register pending write
	// BEFORE sending response so that chunks arriving immediately after won't be dropped.
	if writeReq := msg.GetWriteReq(); writeReq != nil {
		tempDir := m.handler.getTempDir()
		os.MkdirAll(tempDir, 0755)
		tempFile, err := os.CreateTemp(tempDir, "upload-*.tmp")
		if err != nil {
			log.Printf("gRPC Mesh: Failed to create temp file (request_id=%s): %v", msg.RequestId, err)
			// Still let Handle() process to send error response
		} else {
			m.writeMu.Lock()
			m.pendingWrites[msg.RequestId] = &pendingWrite{
				serverUUID: msg.ServerUuid,
				path:       writeReq.Path,
				tempFile:   tempFile,
				tempPath:   tempFile.Name(),
				lastActive: time.Now(),
			}
			m.writeMu.Unlock()
		}
	}

	// ReadReq / SelectiveReadReq: use streaming handler (io.Pipe, constant ~128KB RAM)
	if msg.GetReadReq() != nil || msg.GetSelectiveReadReq() != nil {
		m.handler.HandleStreaming(msg, func(resp *pb.NodeMessage) error {
			return stream.Send(resp)
		})
		return
	}

	// Normal request handling (List, Write, Create, Delete, Rename, Copy)
	responses := m.handler.Handle(msg)
	for _, resp := range responses {
		if err := stream.Send(resp); err != nil {
			log.Printf("gRPC Mesh: Failed to send response (request_id=%s): %v", msg.RequestId, err)
			return
		}
	}
}

// resolveAddr tries private IPs first (500ms dial timeout), falls back to gRPC addr.
func (m *MeshManager) resolveAddr(info CoreInfo) string {
	for _, ip := range info.IPs.Private {
		// Extract port from the advertised gRPC addr
		_, port, err := net.SplitHostPort(info.GRPCAddr)
		if err != nil {
			continue
		}
		addr := net.JoinHostPort(ip, port)
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			log.Printf("gRPC Mesh: Using private IP %s for Core %s", addr, info.ID)
			return addr
		}
	}
	return info.GRPCAddr
}

func (m *MeshManager) closeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, conn := range m.connections {
		conn.cancel()
		conn.conn.Close()
		delete(m.connections, id)
	}
}

// NodeIPInfo holds the Node's network addresses for auth.
type NodeIPInfo struct {
	Public  string
	Private []string
}

func getNodeIPs() NodeIPInfo {
	info := NodeIPInfo{}

	// Public IP via outbound connection
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		info.Public = conn.LocalAddr().(*net.UDPAddr).IP.String()
		conn.Close()
	} else {
		info.Public, _ = os.Hostname()
	}

	// Private IPs
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return info
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		if ip[0] == 10 || (ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) || (ip[0] == 192 && ip[1] == 168) {
			info.Private = append(info.Private, ip.String())
		}
	}

	return info
}

