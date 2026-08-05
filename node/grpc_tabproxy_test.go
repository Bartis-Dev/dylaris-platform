package main

import (
	"net"
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
// It now also RESOLVES rather than formats, and these assertions moved with it:
// one name was never one host. When the container is absent from the network,
// Docker's embedded resolver forwards the name upstream and a wildcard record
// answers with a public address - seen live through the RCON path, which builds
// the same name. So the property to lock is no longer "the string is the
// container name" but "the target is a private address or there is no target".
func TestContainerAddrIsTheOnlyTarget(t *testing.T) {
	// No such container exists in a unit test, so the only correct outcome is a
	// refusal. Producing an address here would mean something answered for a name
	// that cannot be resolved locally.
	got, err := containerAddr("abc-123", 8100)
	if err == nil {
		t.Fatalf("containerAddr resolved a nonexistent container to %q; a name nothing local answers for must not become a target", got)
	}
	if strings.Contains(got, "127.0.0.1") || strings.Contains(got, "localhost") {
		t.Fatalf("containerAddr = %q, which points at the node itself rather than the container", got)
	}
}

// TestContainerAddrNeverLeavesTheContainerForAnyPort walks the port range a tab
// can be configured with. The port is tenant-chosen, so no value of it may
// produce a target outside the container's own private network.
func TestContainerAddrNeverLeavesTheContainerForAnyPort(t *testing.T) {
	for _, port := range []int{1, 22, 80, 2375, 6379, 25500, 25565, 25600, 65535} {
		got, err := containerAddr("srv-uuid", port)
		if err != nil {
			continue // refused, which is the correct answer for a name with no container
		}
		host, _, splitErr := net.SplitHostPort(got)
		if splitErr != nil {
			t.Errorf("port %d produced an unparseable target %q", port, got)
			continue
		}
		ip := net.ParseIP(host)
		if ip == nil || !(ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
			t.Errorf("port %d produced target %q, which is not on a private network", port, got)
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
