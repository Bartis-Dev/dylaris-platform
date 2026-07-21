package main

import (
	"io"
	"net"
	"testing"
	"time"

	pb "dylaris-proto/beam"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeUploadClientStream is a minimal BeamNodeService_UploadFileClient
// (= grpc.ClientStreamingClient[BeamUploadMsg, BeamOpResp]). WriteChunk only
// calls Send and CloseAndRecv, so the embedded nil ClientStream is never used.
type fakeUploadClientStream struct {
	grpc.ClientStream
	sendErr  error
	recvResp *pb.BeamOpResp
	recvErr  error
	closed   bool
}

func (f *fakeUploadClientStream) Send(*pb.BeamUploadMsg) error { return f.sendErr }
func (f *fakeUploadClientStream) CloseAndRecv() (*pb.BeamOpResp, error) {
	f.closed = true
	return f.recvResp, f.recvErr
}

// TestUploadSessionWriteChunkSurfacesTerminatingStatus is the regression guard
// for the fail-fast fix: when Send reports a bare io.EOF (server ended the
// stream early, e.g. a limit rejection), WriteChunk must fetch the real
// terminating status via CloseAndRecv and return THAT, not the EOF that reads
// like a transient network blip and gets retried for 20s.
func TestUploadSessionWriteChunkSurfacesTerminatingStatus(t *testing.T) {
	wantErr := status.Error(codes.ResourceExhausted, "daily upload quota reached")
	fs := &fakeUploadClientStream{sendErr: io.EOF, recvErr: wantErr}
	sess := &UploadSession{stream: fs, cancel: func() {}}

	err := sess.WriteChunk([]byte("x"), 0)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("WriteChunk err = %v (code %v), want ResourceExhausted", err, status.Code(err))
	}
	if !fs.closed {
		t.Error("CloseAndRecv was not called on io.EOF")
	}
}

func TestUploadSessionWriteChunkPassesThroughNonEOFError(t *testing.T) {
	// A non-EOF send error is a live stream error — returned as-is, no CloseAndRecv.
	wantErr := status.Error(codes.Unavailable, "transient")
	fs := &fakeUploadClientStream{sendErr: wantErr}
	sess := &UploadSession{stream: fs, cancel: func() {}}

	if err := sess.WriteChunk([]byte("x"), 0); err != wantErr {
		t.Fatalf("WriteChunk err = %v, want %v", err, wantErr)
	}
	if fs.closed {
		t.Error("CloseAndRecv should not be called on a non-EOF error")
	}
}

func TestUploadSessionWriteChunkSuccess(t *testing.T) {
	fs := &fakeUploadClientStream{}
	sess := &UploadSession{stream: fs, cancel: func() {}}

	if err := sess.WriteChunk([]byte("x"), 0); err != nil {
		t.Fatalf("WriteChunk err = %v, want nil", err)
	}
	if fs.closed {
		t.Error("CloseAndRecv should not be called on success")
	}
}

func TestIsPrivateLANIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.5", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.9", true},
		{"169.254.1.1", true},  // IPv4 link-local
		{"fc00::1", true},      // IPv6 ULA
		{"fe80::1", true},      // IPv6 link-local
		{"8.8.8.8", false},     // public
		{"203.0.113.7", false}, // public (TEST-NET-3)
		{"172.32.0.1", false},  // just outside 172.16/12
		{"0.0.0.0", false},     // unspecified
		{"::", false},          // unspecified IPv6
	}
	for _, c := range cases {
		if got := isPrivateLANIP(net.ParseIP(c.ip)); got != c.want {
			t.Errorf("isPrivateLANIP(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
	if isPrivateLANIP(nil) {
		t.Error("isPrivateLANIP(nil) = true, want false")
	}
}

func TestFilterPrivateLANIPs(t *testing.T) {
	// Public IPs, unparseable strings, and empties are dropped; private hints
	// survive in order.
	in := []string{"10.0.0.5", "8.8.8.8", "192.168.1.9", "203.0.113.7", "not-an-ip", ""}
	got := filterPrivateLANIPs(in)
	want := []string{"10.0.0.5", "192.168.1.9"}
	if len(got) != len(want) {
		t.Fatalf("filterPrivateLANIPs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("filterPrivateLANIPs()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestProbeBeamLANReturnsReachable(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()
	_, port, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	got := probeBeamLAN([]string{"127.0.0.1"}, port)
	want := net.JoinHostPort("127.0.0.1", port)
	if got != want {
		t.Errorf("probeBeamLAN() = %q, want %q", got, want)
	}
}

func TestProbeBeamLANEmptyPort(t *testing.T) {
	// An empty port must fail safe (no LAN match, so the connect chain falls back to the
	// relay) rather than probe a hard-coded fallback port.
	if got := probeBeamLAN([]string{"127.0.0.1"}, ""); got != "" {
		t.Errorf("probeBeamLAN(_, \"\") = %q, want \"\"", got)
	}
}

func TestProbeBeamLANParallelBudget(t *testing.T) {
	// RFC 5737 TEST-NET-1 addresses are unrouted; a dial to them stalls until the
	// per-dial 700ms timeout. Serially, three would cost ~2.1s; concurrently they
	// share one budget. A loose ceiling well under the serial worst case proves the
	// fan-out without asserting an exact duration (timing stays flake-tolerant).
	ips := []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"}
	start := time.Now()
	got := probeBeamLAN(ips, "25523")
	elapsed := time.Since(start)
	if got != "" {
		t.Errorf("probeBeamLAN() = %q, want \"\" (all unreachable)", got)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("probeBeamLAN took %v, want < 1.5s (serial would be ~2.1s)", elapsed)
	}
}
