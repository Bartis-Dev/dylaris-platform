package main

import (
	"net/http"
	"strings"
	"testing"

	pb "dylaris-proto/node"
)

// TestContainerAddrIsTheOnlyTarget locks the proxy's stated security property:
// the target is the server's own container and nothing else.
//
// This test previously asserted the opposite - that a 127.0.0.1:<port>
// candidate followed the container name. The port comes from the tab config,
// so that fallback let the owner of any server aim the node's own loopback at
// a port of their choosing simply by picking one their container does not
// listen on. grpc_rcon.go had already dropped the same fallback, and says why:
// the node is containerized, so its loopback is never the MC container.
func TestContainerAddrIsTheOnlyTarget(t *testing.T) {
	got := containerAddr("abc-123", 8100)
	if want := "mc_abc-123:8100"; got != want {
		t.Fatalf("containerAddr = %q, want %q", got, want)
	}
	if strings.Contains(got, "127.0.0.1") || strings.Contains(got, "localhost") {
		t.Fatalf("containerAddr = %q, which points at the node itself rather than the container", got)
	}
}

// TestContainerAddrNeverLeavesTheContainerForAnyPort walks the port range a tab
// can be configured with: none of them may produce a target other than the
// server's own container.
func TestContainerAddrNeverLeavesTheContainerForAnyPort(t *testing.T) {
	for _, port := range []int{1, 22, 80, 2375, 6379, 25500, 25565, 25600, 65535} {
		got := containerAddr("srv-uuid", port)
		if !strings.HasPrefix(got, "mc_srv-uuid:") {
			t.Errorf("port %d produced target %q, which is not the server's container", port, got)
		}
	}
}

func TestNodeStripHopByHop(t *testing.T) {
	in := []*pb.HttpHeader{
		{Key: "Content-Type", Value: "text/html"},
		{Key: "Connection", Value: "keep-alive"},
		{Key: "Keep-Alive", Value: "timeout=5"},
		{Key: "Transfer-Encoding", Value: "chunked"},
		{Key: "Upgrade", Value: "websocket"},
		{Key: "Proxy-Authorization", Value: "secret"},
		{Key: "X-Custom", Value: "keep-me"},
	}
	got := nodeStripHopByHop(in)
	kept := map[string]bool{}
	for _, h := range got {
		kept[h.Key] = true
	}
	for _, drop := range []string{"Connection", "Keep-Alive", "Transfer-Encoding", "Upgrade", "Proxy-Authorization"} {
		if kept[drop] {
			t.Errorf("hop-by-hop header %q was not stripped", drop)
		}
	}
	for _, keep := range []string{"Content-Type", "X-Custom"} {
		if !kept[keep] {
			t.Errorf("end-to-end header %q was dropped", keep)
		}
	}
}

func TestHeadersToProto(t *testing.T) {
	h := http.Header{}
	h.Add("X-A", "1")
	h.Add("X-A", "2")
	h.Set("X-B", "3")
	got := headersToProto(h)
	var countA, countB int
	for _, kv := range got {
		switch kv.Key {
		case "X-A":
			countA++
		case "X-B":
			countB++
		}
	}
	if countA != 2 {
		t.Errorf("X-A count = %d, want 2 (multi-value flattened)", countA)
	}
	if countB != 1 {
		t.Errorf("X-B count = %d, want 1", countB)
	}
}
