package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	beamauth "dylaris-pkg/beam/auth"
	pb "dylaris-proto/beam"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

// fakeBeamUploadStream is a minimal grpc.ClientStreamingServer for UploadFile.
// UploadFile only calls Context, Recv and SendAndClose, so the embedded nil
// ServerStream is never dereferenced.
type fakeBeamUploadStream struct {
	grpc.ServerStream
	ctx  context.Context
	msgs []*pb.BeamUploadMsg
	idx  int
	resp *pb.BeamOpResp
}

func (f *fakeBeamUploadStream) Context() context.Context { return f.ctx }

func (f *fakeBeamUploadStream) Recv() (*pb.BeamUploadMsg, error) {
	if f.idx >= len(f.msgs) {
		return nil, io.EOF
	}
	m := f.msgs[f.idx]
	f.idx++
	return m, nil
}

func (f *fakeBeamUploadStream) SendAndClose(r *pb.BeamOpResp) error {
	f.resp = r
	return nil
}

// newTestBeamServer builds a beamServer backed by a temp dir, with an
// authenticated peer bound to serverUUID, and returns a matching context.
func newTestBeamServer(t *testing.T) (*beamServer, string, context.Context) {
	t.Helper()
	sm := NewStorageManager(t.TempDir(), nil)
	bs := &beamServer{
		storageMgr: sm,
		throttle:   NewBeamThrottle(context.Background(), nil),
	}
	serverUUID := "11111111-1111-1111-1111-111111111111"
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000}
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: addr})
	bs.serverUUIDByPeer.Store(addr.String(), serverUUID)
	return bs, serverUUID, ctx
}

// TestUploadFile_CreatesMissingSubServerDir pins the server-import enabler: a
// beam upload of .upload.zip into a sub-server dir that does not exist yet
// (exactly the import case, before setup creates it) must succeed by creating
// the parent dir. Before the MkdirAll fix this failed at temp-file creation.
func TestUploadFile_CreatesMissingSubServerDir(t *testing.T) {
	bs, serverUUID, ctx := newTestBeamServer(t)

	const subName = "survival"
	content := []byte("PK\x03\x04 pretend this is a server archive")
	stream := &fakeBeamUploadStream{
		ctx: ctx,
		msgs: []*pb.BeamUploadMsg{
			{Payload: &pb.BeamUploadMsg_Start{Start: &pb.BeamUploadStart{
				Path: subName, Filename: ".upload.zip", TotalSize: int64(len(content)),
			}}},
			{Payload: &pb.BeamUploadMsg_Chunk{Chunk: &pb.BeamUploadChunkData{
				Data: content, Offset: 0,
			}}},
		},
	}

	if err := bs.UploadFile(stream); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if stream.resp == nil || !stream.resp.Success {
		t.Fatalf("upload did not report success: %+v", stream.resp)
	}

	dest := filepath.Join(bs.storageMgr.GetServerDir(serverUUID), subName, ".upload.zip")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading %s: %v", dest, err)
	}
	if string(got) != string(content) {
		t.Errorf("uploaded content mismatch: got %q, want %q", got, content)
	}
}

func TestValidateBeamPathOp(t *testing.T) {
	bs, serverUUID, _ := newTestBeamServer(t)

	cases := []struct {
		name    string
		path    string
		op      string
		wantErr bool
	}{
		{"upload-zip write allowed", "survival/.upload.zip", "write", false},
		{"plain file write allowed", "survival/world.zip", "write", false},
		{"dylaris-prefixed write refused", "survival/.dylaris-secret", "write", true},
		{"active_server write refused", ".active_server", "write", true},
		{"parent traversal refused", "../escape.txt", "write", true},
		{"dylaris-prefixed read allowed", "survival/.dylaris-backups", "read", false},
		{"missing server uuid refused", "survival/x", "write", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uuid := serverUUID
			if c.name == "missing server uuid refused" {
				uuid = ""
			}
			_, err := bs.validateBeamPathOp(c.path, uuid, c.op)
			if (err != nil) != c.wantErr {
				t.Errorf("validateBeamPathOp(%q, %q, %q) err=%v, wantErr=%v", c.path, uuid, c.op, err, c.wantErr)
			}
		})
	}
}

func TestBeamUploadExceedsSizeCap(t *testing.T) {
	cases := []struct {
		name           string
		size, capBytes int64
		want           bool
	}{
		{"zero cap is unlimited", 999999, 0, false},
		{"negative cap is unlimited", 999999, -1, false},
		{"under the cap", 100, 200, false},
		{"exactly at the cap", 200, 200, false},
		{"one byte over", 201, 200, true},
		{"empty upload under cap", 0, 200, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := beamUploadExceedsSizeCap(c.size, c.capBytes); got != c.want {
				t.Errorf("beamUploadExceedsSizeCap(%d, %d) = %v, want %v", c.size, c.capBytes, got, c.want)
			}
		})
	}
}

func TestBeamDailyQuotaExceeded(t *testing.T) {
	cases := []struct {
		name                  string
		used, limit, incoming int64
		want                  bool
	}{
		{"zero limit is unlimited", 100, 0, 999999, false},
		{"negative limit is unlimited", 100, -1, 999999, false},
		{"fits exactly at limit", 100, 200, 100, false},
		{"one byte over", 100, 200, 101, true},
		{"already at limit, empty upload", 200, 200, 0, false},
		{"already over", 300, 200, 0, true},
		{"comfortable headroom", 50, 1000, 100, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := beamDailyQuotaExceeded(c.used, c.limit, c.incoming); got != c.want {
				t.Errorf("beamDailyQuotaExceeded(%d, %d, %d) = %v, want %v", c.used, c.limit, c.incoming, got, c.want)
			}
		})
	}
}

// TestBeamDailyKey pins the counter key format and, critically, that the day is
// normalized to UTC so a node in a positive-offset timezone shares the same
// window as Core.
func TestBeamDailyKey(t *testing.T) {
	cases := []struct {
		name     string
		username string
		day      time.Time
		want     string
	}{
		{
			name:     "utc midday",
			username: "alice",
			day:      time.Date(2026, 7, 21, 15, 4, 5, 0, time.UTC),
			want:     "dylaris:beam:daily:alice:2026-07-21",
		},
		{
			name:     "positive offset rolls back to prior utc day",
			username: "bob",
			day:      time.Date(2026, 7, 22, 0, 30, 0, 0, time.FixedZone("X", 2*3600)),
			want:     "dylaris:beam:daily:bob:2026-07-21",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := beamDailyKey(c.username, c.day); got != c.want {
				t.Errorf("beamDailyKey(%q, %v) = %q, want %q", c.username, c.day, got, c.want)
			}
		})
	}
}

// TestAuthenticateStashesUsername proves the new plumbing: a valid ticket's
// username is stored per-peer and readable at upload time via extractUsername,
// while an empty-username ticket leaves no binding (so distinct anonymous
// sessions never share a daily-quota bucket).
func TestAuthenticateStashesUsername(t *testing.T) {
	const secret = "test-beam-secret"
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40100}
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: addr})

	sign := func(t *testing.T, username string) string {
		t.Helper()
		tok, err := beamauth.SignBeamTicket(secret, beamauth.BeamClaims{
			ServerUUID: "22222222-2222-2222-2222-222222222222",
			Username:   username,
		})
		if err != nil {
			t.Fatalf("SignBeamTicket: %v", err)
		}
		return tok
	}

	t.Run("username is stashed and readable", func(t *testing.T) {
		bs := &beamServer{jwtSecret: secret}
		resp, err := bs.Authenticate(ctx, &pb.BeamAuthReq{Ticket: sign(t, "carol")})
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if !resp.Ok {
			t.Fatalf("Authenticate not ok: %s", resp.Message)
		}
		if got := bs.extractUsername(ctx); got != "carol" {
			t.Errorf("extractUsername = %q, want %q", got, "carol")
		}
	})

	t.Run("empty username leaves no binding", func(t *testing.T) {
		bs := &beamServer{jwtSecret: secret}
		if _, err := bs.Authenticate(ctx, &pb.BeamAuthReq{Ticket: sign(t, "")}); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if got := bs.extractUsername(ctx); got != "" {
			t.Errorf("extractUsername = %q, want empty", got)
		}
	})
}

func TestBeamUploadExceedsDisk(t *testing.T) {
	cases := []struct {
		name                   string
		total, limit, incoming int64
		want                   bool
	}{
		{"zero limit is unlimited", 100, 0, 999999, false},
		{"negative limit is unlimited", 100, -1, 999999, false},
		{"fits exactly at limit", 100, 200, 100, false},
		{"one byte over", 100, 200, 101, true},
		{"already at limit, empty upload", 200, 200, 0, false},
		{"already over", 300, 200, 0, true},
		{"comfortable headroom", 50, 1000, 100, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := beamUploadExceedsDisk(c.total, c.limit, c.incoming); got != c.want {
				t.Errorf("beamUploadExceedsDisk(%d, %d, %d) = %v, want %v", c.total, c.limit, c.incoming, got, c.want)
			}
		})
	}
}
