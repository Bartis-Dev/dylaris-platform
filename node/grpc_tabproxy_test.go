package main

import (
	"net/http"
	"testing"

	pb "dylaris-proto/node"
)

func TestResolveContainerAddrs(t *testing.T) {
	got := resolveContainerAddrs("abc-123", 8100)
	want := []string{"mc_abc-123:8100", "127.0.0.1:8100"}
	if len(got) != len(want) {
		t.Fatalf("resolveContainerAddrs len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("resolveContainerAddrs[%d] = %q, want %q", i, got[i], want[i])
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
