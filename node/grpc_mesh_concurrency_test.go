package main

// Tests for the two WS5 mesh audit fixes:
//   Fix A - the Core->container reassembly buffer is capped (maxWSMessageBytes)
//           and concurrent bridges are bounded (maxWSBridges), so a client
//           streaming endless Fin=false fragments or opening endless bridges
//           cannot OOM the node and take the mesh down for co-located tenants.
//   Fix B - the blocking container dial (WsOpen handshake, HttpProxyReq Do)
//           runs off the shared per-Core read loop, and WsFrames that arrive
//           while a bridge is still dialing are buffered, not dropped.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pb "dylaris-proto/node"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc/metadata"
)

// fakeCoreStream is a minimal grpc.BidiStreamingClient[NodeMessage, NodeMessage]
// that records every message the node sends to Core. Send is called from
// multiple pump goroutines, so it is mutex-guarded.
type fakeCoreStream struct {
	mu   sync.Mutex
	sent []*pb.NodeMessage
}

func (f *fakeCoreStream) Send(m *pb.NodeMessage) error {
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.mu.Unlock()
	return nil
}
func (f *fakeCoreStream) Recv() (*pb.NodeMessage, error) { return nil, context.Canceled }
func (f *fakeCoreStream) Header() (metadata.MD, error)   { return nil, nil }
func (f *fakeCoreStream) Trailer() metadata.MD           { return nil }
func (f *fakeCoreStream) CloseSend() error               { return nil }
func (f *fakeCoreStream) Context() context.Context       { return context.Background() }
func (f *fakeCoreStream) SendMsg(any) error              { return nil }
func (f *fakeCoreStream) RecvMsg(any) error              { return nil }

func (f *fakeCoreStream) messages() []*pb.NodeMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*pb.NodeMessage, len(f.sent))
	copy(out, f.sent)
	return out
}

// TestWSBridgeAppendInbound covers the reassembly step in isolation: normal
// accumulation, flush-on-Fin, opcode carry across continuation frames, and the
// default-to-text opcode. The overflow case is covered separately below.
func TestWSBridgeAppendInbound(t *testing.T) {
	t.Run("single final text frame flushes as-is", func(t *testing.T) {
		b := &wsBridge{}
		payload, op, flush, overflow := b.appendInbound(&pb.WsFrame{Opcode: int32(websocket.TextMessage), Data: []byte("hello"), Fin: true})
		if overflow || !flush {
			t.Fatalf("flush=%v overflow=%v, want flush=true overflow=false", flush, overflow)
		}
		if string(payload) != "hello" {
			t.Errorf("payload = %q, want %q", payload, "hello")
		}
		if op != websocket.TextMessage {
			t.Errorf("opcode = %d, want %d", op, websocket.TextMessage)
		}
		if b.rxBuf != nil {
			t.Errorf("rxBuf must reset after flush, got %d bytes", len(b.rxBuf))
		}
	})

	t.Run("fragments reassemble and carry the opening opcode", func(t *testing.T) {
		b := &wsBridge{}
		// binary opener, not final
		if _, _, flush, _ := b.appendInbound(&pb.WsFrame{Opcode: int32(websocket.BinaryMessage), Data: []byte("ab"), Fin: false}); flush {
			t.Fatal("non-final fragment must not flush")
		}
		// continuation (opcode 0), final
		payload, op, flush, overflow := b.appendInbound(&pb.WsFrame{Opcode: 0, Data: []byte("cd"), Fin: true})
		if overflow || !flush {
			t.Fatalf("flush=%v overflow=%v, want flush=true overflow=false", flush, overflow)
		}
		if string(payload) != "abcd" {
			t.Errorf("reassembled = %q, want %q", payload, "abcd")
		}
		if op != websocket.BinaryMessage {
			t.Errorf("opcode = %d, want %d (opener opcode carried)", op, websocket.BinaryMessage)
		}
	})

	t.Run("zero opcode on a lone frame defaults to text", func(t *testing.T) {
		b := &wsBridge{}
		_, op, flush, _ := b.appendInbound(&pb.WsFrame{Opcode: 0, Data: []byte("x"), Fin: true})
		if !flush || op != websocket.TextMessage {
			t.Fatalf("op=%d flush=%v, want op=%d flush=true", op, flush, websocket.TextMessage)
		}
	})
}

// TestWSBridgeReassemblyBounded is the Fix A DoS regression: feeding endless
// Fin=false fragments must hit the cap and return overflow instead of growing
// rxBuf without bound. rxBuf must never exceed maxWSMessageBytes.
func TestWSBridgeReassemblyBounded(t *testing.T) {
	b := &wsBridge{}
	frag := make([]byte, wsFragmentSize)
	overflowed := false
	for i := 0; i < (maxWSMessageBytes/wsFragmentSize)+5; i++ {
		_, _, flush, overflow := b.appendInbound(&pb.WsFrame{Opcode: int32(websocket.BinaryMessage), Data: frag, Fin: false})
		if len(b.rxBuf) > maxWSMessageBytes {
			t.Fatalf("rxBuf grew to %d bytes, past the %d cap (unbounded reassembly)", len(b.rxBuf), maxWSMessageBytes)
		}
		if flush {
			t.Fatal("a Fin=false fragment must never flush")
		}
		if overflow {
			overflowed = true
			break
		}
	}
	if !overflowed {
		t.Fatal("endless Fin=false fragments never tripped the reassembly cap")
	}
}

// TestHandleWSOpenBridgeLimit covers the Fix A concurrent-bridge bound: at the
// cap a new WsOpen is rejected with a policy WsClose and no bridge is added.
func TestHandleWSOpenBridgeLimit(t *testing.T) {
	m := &MeshManager{wsBridges: make(map[string]*wsBridge)}
	fake := &fakeCoreStream{}
	cc := &coreConnection{stream: fake}

	for i := 0; i < maxWSBridges; i++ {
		m.wsBridges[fmt.Sprintf("pre-%d", i)] = &wsBridge{done: make(chan struct{})}
	}
	before := len(m.wsBridges)

	m.handleWSOpen(cc, "overflow", "srv", &pb.WsOpen{TargetPort: 8080, Path: "/"})

	if got := len(m.wsBridges); got != before {
		t.Fatalf("bridge map grew past the cap: got %d, want %d", got, before)
	}
	if _, ok := m.wsBridges["overflow"]; ok {
		t.Fatal("an over-limit WsOpen must not register a bridge")
	}
	sent := fake.messages()
	if len(sent) != 1 {
		t.Fatalf("want exactly one message to Core, got %d", len(sent))
	}
	cl := sent[0].GetWsClose()
	if cl == nil {
		t.Fatalf("want a WsClose rejection, got %T", sent[0].Payload)
	}
	if cl.Code != 1013 {
		t.Errorf("rejection WsClose code = %d, want 1013", cl.Code)
	}
}

// TestHandleWSOpenPendingDeliversBufferedFrames covers Fix B end to end:
//   - handleWSOpen returns while the container dial is still blocked, so a slow
//     dial for reqA does NOT stall handling of reqB (no head-of-line blocking on
//     the shared read loop).
//   - WsFrames routed for reqA during its pending-connect window are buffered,
//     not dropped, and are delivered to the container in order after activation.
func TestHandleWSOpenPendingDeliversBufferedFrames(t *testing.T) {
	// In-process "container" WS server that records the messages it receives.
	received := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			received <- string(data)
		}
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// Gate the dial so we can observe the pending window and prove the blocked
	// dial for reqA does not stall handling reqB.
	release := make(chan struct{})
	dialStarted := make(chan struct{}, 4)
	orig := wsDialContainer
	defer func() { wsDialContainer = orig }()
	wsDialContainer = func(addrs []string, path string, hdr http.Header) (*websocket.Conn, error) {
		dialStarted <- struct{}{}
		<-release
		c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		return c, err
	}

	m := &MeshManager{wsBridges: make(map[string]*wsBridge)}
	cc := &coreConnection{stream: &fakeCoreStream{}}

	// reqA: the dial goroutine parks on <-release. handleWSOpen still returns.
	m.handleWSOpen(cc, "reqA", "srvA", &pb.WsOpen{TargetPort: 8080, Path: "/a"})
	<-dialStarted

	// reqB is serviced while reqA's dial is still blocked (no head-of-line stall).
	m.handleWSOpen(cc, "reqB", "srvB", &pb.WsOpen{TargetPort: 8080, Path: "/b"})
	select {
	case <-dialStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("reqB was not serviced while reqA's dial was blocked (head-of-line blocking)")
	}

	// Route inbound frames for reqA during its pending window; must buffer.
	for _, s := range []string{"m0", "m1", "m2"} {
		m.routeWSInbound("reqA", &pb.NodeMessage{
			RequestId: "reqA",
			Payload:   &pb.NodeMessage_WsFrame{WsFrame: &pb.WsFrame{Opcode: int32(websocket.TextMessage), Data: []byte(s), Fin: true}},
		})
	}
	m.wsMu.Lock()
	bridge := m.wsBridges["reqA"]
	m.wsMu.Unlock()
	if bridge == nil {
		t.Fatal("reqA bridge must be registered (pending) before activation")
	}
	if got := len(bridge.inbound); got != 3 {
		t.Fatalf("frames buffered during pending = %d, want 3 (must not be dropped)", got)
	}

	// Activate: both dials proceed; reqA flushes its 3 buffered frames in order.
	close(release)

	for i, want := range []string{"m0", "m1", "m2"} {
		select {
		case got := <-received:
			if got != want {
				t.Fatalf("frame %d delivered = %q, want %q (order must be preserved)", i, got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for buffered frame %d (%q) after activation", i, want)
		}
	}

	m.closeAllWSBridges()
}
