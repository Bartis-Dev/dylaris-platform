package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	beamauth "dylaris-pkg/beam/auth"
	pb "dylaris-proto/beam"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const beamChunkSize = 64 * 1024 // 64KB

// beamServer implements the BeamNodeService gRPC interface.
// It listens on :9091 reachable on the container's overlay network so the
// Link sidecar in a separate Swarm container can forward Yamux streams to
// it. Public exposure is still blocked at the Swarm boundary — every RPC
// must present a valid BEAM_JWT_SECRET-signed ticket via Authenticate
// before any file op runs, so overlay-internal reachability is safe.
type beamServer struct {
	pb.UnimplementedBeamNodeServiceServer
	storageMgr *StorageManager
	throttle   *BeamThrottle
	jwtSecret  string // BEAM_JWT_SECRET — must match the gateway's beam-relay
	nodeID     string // local node id; tickets must claim this same id

	// serverUUIDByPeer remembers which server a gRPC peer (= one Beam.exe
	// session's TCP connection) is authenticated for. Authenticate writes
	// it; every other RPC reads it via extractServerUUID. Without this
	// the file-op handlers see an empty serverUUID and reject every call
	// with "server_uuid required" — the symptom Beam.exe surfaces as EOF
	// on the first upload chunk.
	//
	// Entries are keyed by peer address. Beam sessions are short-lived
	// and the same port gets recycled / overwritten on a new Authenticate,
	// so the map stays bounded without explicit cleanup.
	serverUUIDByPeer sync.Map // map[string]string
}

// StartBeamServer starts the BeamNodeService gRPC server on :9091.
//
// Binds to all interfaces (was 127.0.0.1) so a Link in a sibling Swarm
// container — different network namespace, so different loopback — can
// reach it via the overlay using the Node's service name. Without this,
// Link's stream forwards fail at dial time, the relay's Yamux stream
// closes, and Beam.exe surfaces the misleading "error reading server
// preface: EOF". Auth (JWT ticket) gates all RPCs so wider reachability
// on the overlay doesn't open new attack surface.
//
// Also publishes the Node's BeamNodeService endpoint to Redis (key
// `beam:node-endpoint:<NodeID>`) so Link can discover the right
// overlay IP without relying on Docker-DNS service-name conventions.
// This is the canonical discovery path — works in any Swarm topology
// (cross-stack, custom service names, mixed global/replicated), where
// the NodeID is not necessarily a resolvable hostname.
func StartBeamServer(ctx context.Context, rdb *redis.Client, storageMgr *StorageManager, throttle *BeamThrottle, jwtSecret, nodeID string) {
	lis, err := net.Listen("tcp", ":9091")
	if err != nil {
		log.Printf("beam-server: failed to listen on :9091: %v", err)
		return
	}

	srv := grpc.NewServer()
	pb.RegisterBeamNodeServiceServer(srv, &beamServer{
		storageMgr: storageMgr,
		throttle:   throttle,
		jwtSecret:  jwtSecret,
		nodeID:     nodeID,
	})

	log.Println("beam-server: listening on :9091 (reachable via overlay; JWT-gated)")

	// Publish endpoint to Redis so Link can discover us via overlay IP.
	go publishBeamEndpoint(ctx, rdb, nodeID)

	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()

	if err := srv.Serve(lis); err != nil {
		log.Printf("beam-server: serve error: %v", err)
	}
}

// publishBeamEndpoint refreshes a Redis key with this Node's BeamNodeService
// overlay endpoint every 10s (30s TTL). Link reads this key to find the
// right address for its NodeID, sidestepping Docker-DNS service-name
// conventions which break across Swarm stacks (Link in dylaris-gateway
// can't resolve "node-eu-v01" — the Node's NODE_ID is a host hostname,
// not a registered service alias).
//
// Best-effort: a Redis outage just causes Link to fall back to its
// service-name / loopback guess. Beam.exe surfaces "could not reach the
// Beam relay ..." in that case, so the failure mode is visible.
func publishBeamEndpoint(ctx context.Context, rdb *redis.Client, nodeID string) {
	if rdb == nil || strings.TrimSpace(nodeID) == "" {
		return
	}
	const (
		port        = "9091"
		key         = "beam:node-endpoint:"
		ttl         = 30 * time.Second
		refreshTick = 10 * time.Second
	)
	var lastLoggedIP string
	publish := func() {
		ip := overlayIP()
		if ip == "" {
			return
		}
		if err := rdb.Set(ctx, key+nodeID, ip+":"+port, ttl).Err(); err != nil {
			log.Printf("beam-server: endpoint publish failed: %v", err)
			return
		}
		// Log only when the published IP changes (typically once at boot,
		// then on container restart) so the log isn't spammed every 10s.
		if ip != lastLoggedIP {
			log.Printf("beam-server: endpoint published to Redis (%s:%s for node %q)", ip, port, nodeID)
			lastLoggedIP = ip
		}
	}
	publish()
	ticker := time.NewTicker(refreshTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}

// overlayIP returns the container's IP on the Swarm overlay network the
// Link needs to reach us on. The naive net.Dial("udp", "8.8.8.8:80") trick
// returns the wrong interface here — in Swarm the default route goes
// through docker_gwbridge (172.18.0.0/16), but overlay traffic to sibling
// services flows through a separate eth attached to the overlay (10.x).
// We need the latter, otherwise Link tries to dial a gwbridge IP that's
// per-host and not routable from other containers.
//
// Selection: walk all non-loopback IPv4 interfaces and prefer 10.0.0.0/8
// (Swarm overlay default range). Falls back to 172.x/192.x for unusual
// deploys where the overlay subnet isn't in the 10.x range.
func overlayIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	var fallback string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil || ip.IsLoopback() {
			continue
		}
		// 10.0.0.0/8 — Swarm overlay's default range. First match wins.
		if ip[0] == 10 {
			return ip.String()
		}
		// 172.16.0.0/12 or 192.168.0.0/16 — saved as fallback for
		// non-standard overlay subnets. The 172.18.x gwbridge IPs end
		// up here too; we only use them if no 10.x is found.
		if fallback == "" && (ip[0] == 172 || ip[0] == 192) {
			fallback = ip.String()
		}
	}
	return fallback
}

// validateBeamPath ensures the path stays within the server's data directory.
func (s *beamServer) validateBeamPath(reqPath, serverUUID string) (string, error) {
	if serverUUID == "" {
		return "", fmt.Errorf("server_uuid required")
	}

	dataPath := s.storageMgr.GetServerDir(serverUUID)
	fullPath := filepath.Join(dataPath, reqPath)
	cleanPath := filepath.Clean(fullPath)

	if !strings.HasPrefix(cleanPath, filepath.Clean(dataPath)) {
		return "", fmt.Errorf("access denied: path traversal")
	}
	return cleanPath, nil
}

// ─── Auth ────────────────────────────────────────────────────────────

func (s *beamServer) Authenticate(ctx context.Context, req *pb.BeamAuthReq) (*pb.BeamAuthResp, error) {
	if s.jwtSecret == "" {
		// Defence in depth: empty secret means no validator is configured,
		// so we must refuse rather than accept blindly.
		return &pb.BeamAuthResp{Ok: false, Message: "node beam auth not configured"}, nil
	}
	claims, err := beamauth.ValidateBeamTicket(s.jwtSecret, req.Ticket)
	if err != nil {
		return &pb.BeamAuthResp{Ok: false, Message: "invalid ticket: " + err.Error()}, nil
	}
	// Node-binding: the relay routes by node_id, but a stolen ticket for
	// another node should still be rejected at the destination.
	if s.nodeID != "" && claims.NodeID != s.nodeID {
		return &pb.BeamAuthResp{Ok: false, Message: "ticket bound to a different node"}, nil
	}
	// Remember which server this gRPC connection is allowed to touch.
	// extractServerUUID reads the same key on every subsequent RPC from
	// the same Beam.exe session.
	if p, ok := peer.FromContext(ctx); ok && p != nil && p.Addr != nil {
		s.serverUUIDByPeer.Store(p.Addr.String(), claims.ServerUUID)
	}
	return &pb.BeamAuthResp{
		Ok:         true,
		ServerUuid: claims.ServerUUID,
	}, nil
}

// ─── File Operations ─────────────────────────────────────────────────

func (s *beamServer) ListFiles(ctx context.Context, req *pb.BeamFileListReq) (*pb.BeamFileListResp, error) {
	serverUUID := s.extractServerUUID(ctx)
	dirPath, err := s.validateBeamPath(req.Path, serverUUID)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &pb.BeamFileListResp{Files: []*pb.BeamFileInfo{}}, nil
		}
		return nil, status.Errorf(codes.Internal, "read dir: %v", err)
	}

	var files []*pb.BeamFileInfo
	for _, e := range entries {
		if e.Name() == ".active_server" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, &pb.BeamFileInfo{
			Name:     e.Name(),
			IsDir:    e.IsDir(),
			Size:     info.Size(),
			Modified: info.ModTime().Unix(),
		})
	}

	return &pb.BeamFileListResp{Files: files}, nil
}

func (s *beamServer) ReadFileContent(ctx context.Context, req *pb.BeamFileReadReq) (*pb.BeamFileContentResp, error) {
	serverUUID := s.extractServerUUID(ctx)
	filePath, err := s.validateBeamPath(req.Path, serverUUID)
	if err != nil {
		return &pb.BeamFileContentResp{Success: false, Message: err.Error()}, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return &pb.BeamFileContentResp{Success: false, Message: err.Error()}, nil
	}

	return &pb.BeamFileContentResp{Success: true, Content: string(data)}, nil
}

func (s *beamServer) SaveFileContent(ctx context.Context, req *pb.BeamFileSaveReq) (*pb.BeamOpResp, error) {
	serverUUID := s.extractServerUUID(ctx)
	filePath, err := s.validateBeamPath(req.Path, serverUUID)
	if err != nil {
		return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
	}

	if err := os.WriteFile(filePath, []byte(req.Content), 0644); err != nil {
		return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
	}

	return &pb.BeamOpResp{Success: true, Message: "saved"}, nil
}

func (s *beamServer) CreateFile(ctx context.Context, req *pb.BeamFileCreateReq) (*pb.BeamOpResp, error) {
	serverUUID := s.extractServerUUID(ctx)
	filePath, err := s.validateBeamPath(req.Path, serverUUID)
	if err != nil {
		return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
	}

	if req.IsDir {
		if err := os.MkdirAll(filePath, 0755); err != nil {
			return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
		}
		f, err := os.Create(filePath)
		if err != nil {
			return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
		}
		f.Close()
	}

	return &pb.BeamOpResp{Success: true, Message: "created"}, nil
}

func (s *beamServer) DeleteFile(ctx context.Context, req *pb.BeamFileDeleteReq) (*pb.BeamOpResp, error) {
	serverUUID := s.extractServerUUID(ctx)
	filePath, err := s.validateBeamPath(req.Path, serverUUID)
	if err != nil {
		return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
	}

	if err := os.RemoveAll(filePath); err != nil {
		return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
	}

	return &pb.BeamOpResp{Success: true, Message: "deleted"}, nil
}

func (s *beamServer) RenameFile(ctx context.Context, req *pb.BeamFileRenameReq) (*pb.BeamOpResp, error) {
	serverUUID := s.extractServerUUID(ctx)
	oldPath, err := s.validateBeamPath(req.OldPath, serverUUID)
	if err != nil {
		return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
	}

	// Protect .active_server
	if filepath.Base(oldPath) == ".active_server" {
		return &pb.BeamOpResp{Success: false, Message: "cannot rename .active_server"}, nil
	}

	newPath := filepath.Join(filepath.Dir(oldPath), req.NewName)

	if err := os.Rename(oldPath, newPath); err != nil {
		return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
	}

	return &pb.BeamOpResp{Success: true, Message: "renamed"}, nil
}

func (s *beamServer) CopyFile(ctx context.Context, req *pb.BeamFileCopyReq) (*pb.BeamOpResp, error) {
	serverUUID := s.extractServerUUID(ctx)
	srcPath, err := s.validateBeamPath(req.SrcPath, serverUUID)
	if err != nil {
		return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
	}
	dstPath, err := s.validateBeamPath(req.DstPath, serverUUID)
	if err != nil {
		return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
	}

	stat, err := os.Stat(srcPath)
	if err != nil {
		return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
	}

	if stat.IsDir() {
		if err := copyDir(srcPath, dstPath); err != nil {
			return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
		}
	} else {
		if err := copyFile(srcPath, dstPath); err != nil {
			return &pb.BeamOpResp{Success: false, Message: err.Error()}, nil
		}
	}

	return &pb.BeamOpResp{Success: true, Message: "copied"}, nil
}

// ─── Streaming Downloads ─────────────────────────────────────────────

func (s *beamServer) DownloadFile(req *pb.BeamDownloadReq, stream grpc.ServerStreamingServer[pb.BeamChunk]) error {
	ctx := stream.Context()
	serverUUID := s.extractServerUUID(ctx)
	filePath, err := s.validateBeamPath(req.Path, serverUUID)
	if err != nil {
		return status.Error(codes.PermissionDenied, err.Error())
	}

	stat, err := os.Stat(filePath)
	if err != nil {
		return status.Errorf(codes.NotFound, "file not found")
	}

	if stat.IsDir() {
		// TODO: zip directory and stream
		return status.Errorf(codes.Unimplemented, "directory download not yet implemented in Beam")
	}

	f, err := os.Open(filePath)
	if err != nil {
		return status.Errorf(codes.Internal, "open file: %v", err)
	}
	defer f.Close()

	buf := make([]byte, beamChunkSize)
	var offset int64
	first := true

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			// Throttle bandwidth
			if err := s.throttle.WaitN(ctx, n); err != nil {
				return status.Errorf(codes.Canceled, "throttle: %v", err)
			}

			chunk := &pb.BeamChunk{
				Data:   buf[:n],
				Offset: offset,
			}
			if first {
				chunk.Filename = filepath.Base(filePath)
				chunk.TotalSize = stat.Size()
				first = false
			}

			if err := stream.Send(chunk); err != nil {
				return err
			}
			offset += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return status.Errorf(codes.Internal, "read file: %v", readErr)
		}
	}

	return nil
}

func (s *beamServer) UploadFile(stream grpc.ClientStreamingServer[pb.BeamUploadMsg, pb.BeamOpResp]) error {
	ctx := stream.Context()
	serverUUID := s.extractServerUUID(ctx)

	var destPath string
	var tmpFile *os.File

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		switch p := msg.Payload.(type) {
		case *pb.BeamUploadMsg_Start:
			remotePath := filepath.Join(p.Start.Path, p.Start.Filename)
			resolved, err := s.validateBeamPath(remotePath, serverUUID)
			if err != nil {
				return status.Error(codes.PermissionDenied, err.Error())
			}
			destPath = resolved

			// Create temp file
			tmpFile, err = os.CreateTemp(filepath.Dir(destPath), ".beam-upload-*")
			if err != nil {
				return status.Errorf(codes.Internal, "create temp: %v", err)
			}

		case *pb.BeamUploadMsg_Chunk:
			if tmpFile == nil {
				return status.Errorf(codes.FailedPrecondition, "no upload started")
			}

			// Throttle bandwidth
			if err := s.throttle.WaitN(ctx, len(p.Chunk.Data)); err != nil {
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				return status.Errorf(codes.Canceled, "throttle: %v", err)
			}

			if _, err := tmpFile.WriteAt(p.Chunk.Data, p.Chunk.Offset); err != nil {
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				return status.Errorf(codes.Internal, "write chunk: %v", err)
			}
		}
	}

	if tmpFile != nil {
		tmpFile.Close()
		if destPath != "" {
			if err := os.Rename(tmpFile.Name(), destPath); err != nil {
				os.Remove(tmpFile.Name())
				return stream.SendAndClose(&pb.BeamOpResp{Success: false, Message: err.Error()})
			}
		}
	}

	return stream.SendAndClose(&pb.BeamOpResp{Success: true, Message: "uploaded"})
}

func (s *beamServer) DownloadSelective(req *pb.BeamSelectiveReq, stream grpc.ServerStreamingServer[pb.BeamChunk]) error {
	// TODO: implement selective zip download (reuse StreamHandler pattern)
	return status.Errorf(codes.Unimplemented, "selective download not yet implemented in Beam")
}

// ─── Quota ───────────────────────────────────────────────────────────

func (s *beamServer) GetTransferQuota(ctx context.Context, req *pb.BeamQuotaReq) (*pb.BeamQuotaResp, error) {
	return &pb.BeamQuotaResp{
		DailyUsed:  0, // TODO: track daily transfer in Redis
		DailyLimit: 0, // 0 = unlimited
		BwLimit:    s.throttle.Limit(),
	}, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────

// extractServerUUID extracts the server UUID from the gRPC context metadata.
// TODO: This will be populated by the Authenticate interceptor from the ticket claims.
// extractServerUUID returns the server-UUID that Authenticate stashed for
// this gRPC peer. One Beam.exe session = one TCP connection from Link to
// the Node = one stable peer address, so this is reliable for the
// lifetime of a session.
//
// Returns "" when the peer hasn't authenticated yet — validateBeamPath
// then refuses with "server_uuid required" which surfaces upstream as a
// PermissionDenied gRPC error.
func (s *beamServer) extractServerUUID(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil || p.Addr == nil {
		return ""
	}
	v, ok := s.serverUUIDByPeer.Load(p.Addr.String())
	if !ok {
		return ""
	}
	uuid, _ := v.(string)
	return uuid
}

// copyDir and copyFile are defined in installer.go — reused here.

// Compile-time check
var _ = time.Now // used for future idle timeout tracking
