package nodegrpc

import (
	"sync"
	"testing"

	pb "dylaris-proto/node"
)

// addPending registers a response channel the way SendRequest/SendRequestStreaming
// do. Those two cannot be used here because they call conn.Send, which needs a
// real gRPC stream; this test only exercises the channel lifetime, so it takes
// the same lock and writes the same map entry directly.
func addPending(conn *NodeConnection, requestID string) chan *pb.NodeMessage {
	ch := make(chan *pb.NodeMessage, 1)
	conn.mu.Lock()
	conn.pending[requestID] = ch
	conn.mu.Unlock()
	return ch
}

// TestPendingChannelsAreRemovedWhenClosed pins the contract that makes
// RouteResponse's lock discipline sufficient: every path that closes a pending
// channel must also make that channel unreachable through the map, under the
// same mutex. If a closer ever left a closed channel in pending, RouteResponse
// would find it and send on it, which panics.
//
// The three closers are covered as a table. Register appears because a node
// reconnecting under the same id closes the outgoing connection's channels from
// a DIFFERENT goroutine than the one routing on it, which is the case that made
// the send-after-unlock window reachable in the first place.
func TestPendingChannelsAreRemovedWhenClosed(t *testing.T) {
	tests := []struct {
		name  string
		close func(r *Registry, conn *NodeConnection)
	}{
		{
			name:  "reconnect under the same node id",
			close: func(r *Registry, _ *NodeConnection) { r.Register(1, "token", nil) },
		},
		{
			name:  "node disconnects",
			close: func(r *Registry, _ *NodeConnection) { r.Unregister(1) },
		},
		{
			name:  "streaming transfer finishes",
			close: func(_ *Registry, conn *NodeConnection) { conn.CloseStreamingRequest("req-1") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			conn := r.Register(1, "token", nil)
			ch := addPending(conn, "req-1")

			tt.close(r, conn)

			// The channel must be closed: a caller ranging over it has to be
			// released, not left blocked forever.
			if _, open := <-ch; open {
				t.Fatal("pending channel = still open after close, want closed")
			}

			// And it must no longer be reachable, or RouteResponse would send
			// on it. This is the half that keeps the send safe under the lock.
			if conn.RouteResponse(&pb.NodeMessage{RequestId: "req-1"}) {
				t.Fatal("RouteResponse on a closed request = true, want false (channel still reachable through pending)")
			}
		})
	}
}

// TestRouteResponseSurvivesAReconnectRace exercises the concurrent shape that
// the lock discipline exists for: one goroutine routing a response while
// another reconnects the same node id and closes the channel out from under it.
//
// This is a smoke check, NOT a proof. It cannot fail on correct code, but it
// only catches the broken version when the two goroutines happen to interleave
// inside the window, so a single green run does not establish the fix. What
// establishes it is that every closer drops the map entry under conn.mu
// (pinned by TestPendingChannelsAreRemovedWhenClosed) and that RouteResponse
// now holds that mutex across its send. Run with -race.
func TestRouteResponseSurvivesAReconnectRace(t *testing.T) {
	const rounds = 500

	for i := 0; i < rounds; i++ {
		r := NewRegistry()
		conn := r.Register(1, "token", nil)
		addPending(conn, "req-1")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			conn.RouteResponse(&pb.NodeMessage{RequestId: "req-1"})
		}()
		go func() {
			defer wg.Done()
			r.Register(1, "token", nil)
		}()
		wg.Wait()
	}
}
