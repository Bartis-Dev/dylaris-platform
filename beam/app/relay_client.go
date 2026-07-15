package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pb "dylaris-proto/beam"
	"dylaris-pkg/protocol"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	// Side-effect import registers the gzip compressor so call sites can
	// opt in with grpc.UseCompressor("gzip").
	_ "google.golang.org/grpc/encoding/gzip"
)

// alreadyCompressed lists file extensions whose payloads gain nothing from
// gRPC-level gzip. Saves CPU on both ends — and a stalled gzip stream is
// worse than the uncompressed baseline. Lowercased lookup.
var alreadyCompressed = map[string]bool{
	".jar":  true,
	".zip":  true,
	".7z":   true,
	".rar":  true,
	".gz":   true,
	".tgz":  true,
	".bz2":  true,
	".xz":   true,
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".webp": true,
	".mp4":  true,
	".webm": true,
	".mp3":  true,
	".ogg":  true,
}

// compressorForPath returns the gRPC call options to use for downloading
// `name`. Text-like content gets gzip; pre-compressed binaries pass through
// raw to avoid spending CPU on negative-return compression.
func compressorForPath(name string) []grpc.CallOption {
	ext := strings.ToLower(filepath.Ext(name))
	if alreadyCompressed[ext] {
		return nil
	}
	return []grpc.CallOption{grpc.UseCompressor("gzip")}
}

// BeamNodeClient holds an authenticated gRPC connection to a Node's BeamNodeService
// routed through the BeamRelay tunnel.
type BeamNodeClient struct {
	conn       *grpc.ClientConn
	svc        pb.BeamNodeServiceClient
	ServerUUID string
}

// ConnectBeamNode dials the relay, authenticates with the JWT ticket, and
// returns a ready BeamNodeClient. If relayAddr has no port, :25550 is used.
// relayTLSSkipVerify reports whether to skip relay TLS verification. Defaults
// to false (verify the relay certificate). Set BEAM_TLS_SKIP_VERIFY=true for
// dev relays that present a self-signed certificate.
func relayTLSSkipVerify() bool {
	v := os.Getenv("BEAM_TLS_SKIP_VERIFY")
	return v == "true" || v == "1"
}

func ConnectBeamNode(relayAddr, ticket string) (*BeamNodeClient, error) {
	if _, _, err := net.SplitHostPort(relayAddr); err != nil {
		relayAddr = net.JoinHostPort(relayAddr, "25550")
	}

	conn, err := grpc.NewClient("passthrough:///beam-node",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			// Bound the dial: without a timeout a dropped SYN (firewall
			// DROP rather than REJECT) hangs until the RPC deadline,
			// surfacing as an opaque "DeadlineExceeded".
			d := &net.Dialer{Timeout: 10 * time.Second}
			tlsConn, err := tls.DialWithDialer(d, "tcp", relayAddr, &tls.Config{
				InsecureSkipVerify: relayTLSSkipVerify(),
			})
			if err != nil {
				return nil, fmt.Errorf("relay dial: %w", err)
			}
			if err := protocol.WriteBeamHeader(tlsConn, ticket); err != nil {
				tlsConn.Close()
				return nil, fmt.Errorf("beam header: %w", err)
			}
			return tlsConn, nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc: %w", err)
	}

	svc := pb.NewBeamNodeServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := svc.Authenticate(ctx, &pb.BeamAuthReq{Ticket: ticket})
	if err != nil {
		conn.Close()
		// Raw gRPC dial failures embed the OS socket error, which on
		// Windows is localized (German, etc.). The clean message keyed
		// off the gRPC status code keeps the user-visible string
		// English; the underlying err is appended verbatim so admins
		// (with Developer Mode on) can read what actually failed —
		// DNS, RST, TLS handshake, etc. — instead of guessing.
		//   Unavailable      → port refused / relay down / dial fail
		//   DeadlineExceeded → packets dropped (firewall) or TLS stalled
		switch status.Code(err) {
		case codes.Unavailable, codes.DeadlineExceeded:
			return nil, fmt.Errorf("could not reach the Beam relay at %s — it may be offline, or its port is closed or blocked by a firewall (underlying: %v)", relayAddr, err)
		default:
			return nil, fmt.Errorf("relay authentication failed: %v", err)
		}
	}
	if !resp.Ok {
		conn.Close()
		return nil, fmt.Errorf("auth rejected: %s", resp.Message)
	}

	return &BeamNodeClient{conn: conn, svc: svc, ServerUUID: resp.ServerUuid}, nil
}

// ConnectBeamNodeDirect dials a Node's BeamNodeService DIRECTLY over the LAN and
// authenticates with the same JWT ticket. This is the LAN fast-path. The hop is
// TLS-encrypted and the node's self-signed certificate is PINNED by the
// fingerprint Core derived from the shared beam secret + node ID — so a passive
// sniffer learns nothing and an active MITM (no matching cert) is rejected. The
// JWT ticket still gates auth and is node+server bound, so this grants no more
// than the relay path.
func ConnectBeamNodeDirect(addr, ticket, fingerprint string) (*BeamNodeClient, error) {
	if fingerprint == "" {
		return nil, fmt.Errorf("missing LAN cert fingerprint; refusing unpinned direct dial")
	}
	want, err := hex.DecodeString(fingerprint)
	if err != nil {
		return nil, fmt.Errorf("bad fingerprint: %w", err)
	}

	// Pin by leaf-cert SHA-256. We skip the normal chain/hostname checks (the
	// node serves a deterministic self-signed cert, not a CA-issued one) and
	// instead require the presented leaf to match the expected fingerprint
	// exactly — equivalent to certificate pinning.
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS13, // node serves an ed25519 leaf (TLS 1.3)
		InsecureSkipVerify: true,             // chain/name checks replaced by the pin below
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no certificate presented")
			}
			sum := sha256.Sum256(rawCerts[0])
			if subtle.ConstantTimeCompare(sum[:], want) != 1 {
				return fmt.Errorf("LAN certificate fingerprint mismatch (possible MITM); refusing")
			}
			return nil
		},
	}

	conn, err := grpc.NewClient("passthrough:///beam-node-lan",
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			d := &net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "tcp", addr)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc: %w", err)
	}

	svc := pb.NewBeamNodeServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := svc.Authenticate(ctx, &pb.BeamAuthReq{Ticket: ticket})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("lan auth: %w", err)
	}
	if !resp.Ok {
		conn.Close()
		return nil, fmt.Errorf("auth rejected: %s", resp.Message)
	}
	return &BeamNodeClient{conn: conn, svc: svc, ServerUUID: resp.ServerUuid}, nil
}

// probeBeamLAN returns the first ip:port that accepts a TCP connection within a short
// budget, or "" if none do. A cheap reachability check run before the gRPC dial so an
// unreachable LAN hint costs at most one budget before we fall back to the relay.
//
// The dials run CONCURRENTLY so the total probe cost stays within one budget instead
// of len(ips) x budget: the preference chain now probes LAN on EVERY connect (even
// when a relay is present), so a serial probe of several unreachable hints would
// delay the relay fallback unacceptably.
func probeBeamLAN(ips []string, port string) string {
	// The sole caller (dialLANFastpath) always supplies the pinned-TLS beam port
	// (BEAM_LAN_PORT, default 25523). An empty port fails safe - no LAN match, so the
	// connect chain falls back to the relay - rather than probing a hard-coded port.
	if port == "" || len(ips) == 0 {
		return ""
	}
	const budget = 700 * time.Millisecond
	found := make(chan string, len(ips)) // buffered so late successes never block
	var wg sync.WaitGroup
	for _, ip := range ips {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", addr, budget)
			if err == nil {
				conn.Close()
				select {
				case found <- addr:
				default:
				}
			}
		}(net.JoinHostPort(ip, port))
	}
	// Close found once every dial has settled so a range/receive terminates when
	// nothing is reachable. Each dial is capped at `budget`, so this resolves in
	// ~budget regardless of how many IPs are unreachable.
	go func() {
		wg.Wait()
		close(found)
	}()
	addr, ok := <-found
	if !ok {
		return "" // channel closed with no success
	}
	return addr
}

func (c *BeamNodeClient) Close() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// Ping does a lightweight RPC roundtrip to verify the tunnel is alive.
// Used by the App's background health-check goroutine to detect relay
// drops and reconnect to a fresh one before the user notices. Returns
// the gRPC error verbatim so callers can decide how to react.
func (c *BeamNodeClient) Ping(ctx context.Context) error {
	_, err := c.svc.GetTransferQuota(ctx, &pb.BeamQuotaReq{})
	return err
}

// ─── File Operations ──────────────────────────────────────────────────

func (c *BeamNodeClient) ListFiles(path string) (*FileListResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.svc.ListFiles(ctx, &pb.BeamFileListReq{Path: path})
	if err != nil {
		return nil, err
	}

	files := make([]FileEntry, len(resp.Files))
	for i, f := range resp.Files {
		files[i] = FileEntry{Name: f.Name, IsDir: f.IsDir, Size: f.Size}
	}
	return &FileListResult{Success: true, Files: files}, nil
}

func (c *BeamNodeClient) GetFileContent(path string) (*FileContentResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.svc.ReadFileContent(ctx, &pb.BeamFileReadReq{Path: path})
	if err != nil {
		return nil, err
	}
	return &FileContentResult{Success: resp.Success, Content: resp.Content, Message: resp.Message}, nil
}

func (c *BeamNodeClient) SaveFile(path, content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.svc.SaveFileContent(ctx, &pb.BeamFileSaveReq{Path: path, Content: content})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Message)
	}
	return nil
}

func (c *BeamNodeClient) CreateFile(path string, isDir bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := c.svc.CreateFile(ctx, &pb.BeamFileCreateReq{Path: path, IsDir: isDir})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Message)
	}
	return nil
}

func (c *BeamNodeClient) DeleteFile(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := c.svc.DeleteFile(ctx, &pb.BeamFileDeleteReq{Path: path})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Message)
	}
	return nil
}

func (c *BeamNodeClient) RenameFile(oldPath, newName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := c.svc.RenameFile(ctx, &pb.BeamFileRenameReq{OldPath: oldPath, NewName: newName})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Message)
	}
	return nil
}

func (c *BeamNodeClient) CopyFile(srcPath, dstPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.svc.CopyFile(ctx, &pb.BeamFileCopyReq{SrcPath: srcPath, DstPath: dstPath})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Message)
	}
	return nil
}

// ─── Streaming Uploads ───────────────────────────────────────────────

// UploadSession is a single in-flight chunked upload. Created by
// UploadStart, fed with WriteChunk, completed (or aborted) with Finish
// or Cancel. The gRPC stream is held open between calls so the Node
// can stream chunks straight into its temp file — no buffering, no
// HTTP body limit on the Core to worry about.
type UploadSession struct {
	stream pb.BeamNodeService_UploadFileClient
	cancel context.CancelFunc
}

// UploadStart opens a chunked upload stream and sends the BeamUploadStart
// header. Returns a session the caller drives via WriteChunk + Finish.
// No timeout on the underlying context — uploads can be arbitrarily large
// and the Node enforces its own bandwidth throttle.
//
// uploadID is attached as gRPC metadata (header "x-beam-upload-id") so
// the Node can keep the temp file across stream drops keyed by id. An
// empty uploadID falls back to legacy non-resumable behaviour (random
// temp suffix, drop on disconnect). Resuming an upload is just calling
// UploadStart again with the SAME uploadID — the Node reattaches to the
// existing temp file at its current size, and subsequent WriteChunk
// calls with explicit offsets fill in / overwrite as needed.
func (c *BeamNodeClient) UploadStart(uploadID, path, filename, strategy string, totalSize int64) (*UploadSession, error) {
	ctx, cancel := context.WithCancel(context.Background())
	if uploadID != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-beam-upload-id", uploadID)
	}
	stream, err := c.svc.UploadFile(ctx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open upload stream: %w", err)
	}
	if err := stream.Send(&pb.BeamUploadMsg{
		Payload: &pb.BeamUploadMsg_Start{
			Start: &pb.BeamUploadStart{
				Path:      path,
				Filename:  filename,
				TotalSize: totalSize,
				Strategy:  strategy,
			},
		},
	}); err != nil {
		cancel()
		return nil, fmt.Errorf("send start: %w", err)
	}
	return &UploadSession{stream: stream, cancel: cancel}, nil
}

func (s *UploadSession) WriteChunk(data []byte, offset int64) error {
	return s.stream.Send(&pb.BeamUploadMsg{
		Payload: &pb.BeamUploadMsg_Chunk{
			Chunk: &pb.BeamUploadChunkData{Data: data, Offset: offset},
		},
	})
}

func (s *UploadSession) Finish() error {
	resp, err := s.stream.CloseAndRecv()
	s.cancel()
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Message)
	}
	return nil
}

func (s *UploadSession) Cancel() {
	s.cancel()
}

// ─── Streaming Downloads ──────────────────────────────────────────────

func (c *BeamNodeClient) DownloadFile(path string, isDir bool, savePath string, onProgress func(loaded, total int64)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Directory downloads are server-zipped — already compressed by the time
	// they hit the wire — so skip gzip there. Single files get the
	// extension-based heuristic.
	var opts []grpc.CallOption
	if !isDir {
		opts = compressorForPath(path)
	}
	stream, err := c.svc.DownloadFile(ctx, &pb.BeamDownloadReq{Path: path, ZipIfDir: isDir}, opts...)
	if err != nil {
		return err
	}
	return writeChunksToFile(stream, savePath, onProgress)
}

func (c *BeamNodeClient) SelectiveDownload(basePath string, selected []string, selectAll bool, savePath string, onProgress func(loaded, total int64)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Selective downloads always produce a zip on the server side — no point
	// asking gRPC to re-compress.
	stream, err := c.svc.DownloadSelective(ctx, &pb.BeamSelectiveReq{
		BasePath:  basePath,
		Selected:  selected,
		SelectAll: selectAll,
	})
	if err != nil {
		return err
	}
	return writeChunksToFile(stream, savePath, onProgress)
}

// chunkStream is satisfied by both DownloadFile and DownloadSelective streams.
type chunkStream interface {
	Recv() (*pb.BeamChunk, error)
}

func writeChunksToFile(stream chunkStream, savePath string, onProgress func(loaded, total int64)) error {
	// Ensure target directory exists
	if err := os.MkdirAll(filepath.Dir(savePath), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	f, err := os.Create(savePath)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer f.Close()

	var totalSize int64
	var loaded int64

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if len(chunk.Data) > 0 {
			if _, err := f.Write(chunk.Data); err != nil {
				return err
			}
			if chunk.TotalSize > 0 && totalSize == 0 {
				totalSize = chunk.TotalSize
			}
			loaded += int64(len(chunk.Data))
			if onProgress != nil {
				onProgress(loaded, totalSize)
			}
		}
	}
	return nil
}
