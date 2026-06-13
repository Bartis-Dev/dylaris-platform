package nodegrpc

import (
	"fmt"
	"sync"
	"time"

	pb "dylaris-proto/node"
)

// NodeConnection represents an active gRPC stream to a single Node.
type NodeConnection struct {
	NodeID    int
	NodeToken string
	Stream    pb.NodeService_NodeConnectServer

	// pending maps request_id → response channel.
	// The stream reader goroutine routes incoming messages to the correct waiter.
	pending map[string]chan *pb.NodeMessage
	mu      sync.Mutex
}

// Registry is thread-safe for concurrent access from HTTP handlers.
type Registry struct {
	connections map[int]*NodeConnection
	mu          sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{
		connections: make(map[int]*NodeConnection),
	}
}

// Register adds a new Node connection to the registry.
func (r *Registry) Register(nodeID int, token string, stream pb.NodeService_NodeConnectServer) *NodeConnection {
	conn := &NodeConnection{
		NodeID:    nodeID,
		NodeToken: token,
		Stream:    stream,
		pending:   make(map[string]chan *pb.NodeMessage),
	}

	r.mu.Lock()
	if old, ok := r.connections[nodeID]; ok {
		old.mu.Lock()
		for _, ch := range old.pending {
			close(ch)
		}
		old.pending = nil
		old.mu.Unlock()
	}
	r.connections[nodeID] = conn
	r.mu.Unlock()

	return conn
}

// Unregister removes a Node connection from the registry.
func (r *Registry) Unregister(nodeID int) {
	r.mu.Lock()
	if conn, ok := r.connections[nodeID]; ok {
		conn.mu.Lock()
		for _, ch := range conn.pending {
			close(ch)
		}
		conn.pending = nil
		conn.mu.Unlock()
		delete(r.connections, nodeID)
	}
	r.mu.Unlock()
}

// GetConnection returns the active connection for a Node, if any.
func (r *Registry) GetConnection(nodeID int) (*NodeConnection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conn, ok := r.connections[nodeID]
	return conn, ok
}

// IsConnected returns true if a Node has an active gRPC connection.
func (r *Registry) IsConnected(nodeID int) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.connections[nodeID]
	return ok
}

// SendRequest sends a message over the gRPC stream and waits for a response
// matching the same request_id. Returns error on timeout or if Node is not connected.
func (r *Registry) SendRequest(nodeID int, msg *pb.NodeMessage, timeout time.Duration) (*pb.NodeMessage, error) {
	conn, ok := r.GetConnection(nodeID)
	if !ok {
		return nil, fmt.Errorf("node %d not connected", nodeID)
	}

	ch := make(chan *pb.NodeMessage, 1)
	conn.mu.Lock()
	if conn.pending == nil {
		conn.mu.Unlock()
		return nil, fmt.Errorf("node %d connection closed", nodeID)
	}
	conn.pending[msg.RequestId] = ch
	conn.mu.Unlock()

	defer func() {
		conn.mu.Lock()
		delete(conn.pending, msg.RequestId)
		conn.mu.Unlock()
	}()

	if err := conn.Stream.Send(msg); err != nil {
		return nil, fmt.Errorf("failed to send to node %d: %w", nodeID, err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("node %d connection closed while waiting", nodeID)
		}
		return resp, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for response from node %d (request %s)", nodeID, msg.RequestId)
	}
}

// SendRequestStreaming sends a message and returns a channel that will receive
// all response messages for this request_id (for chunked transfers).
// Caller MUST read from the channel until it's closed.
func (r *Registry) SendRequestStreaming(nodeID int, msg *pb.NodeMessage) (<-chan *pb.NodeMessage, error) {
	conn, ok := r.GetConnection(nodeID)
	if !ok {
		return nil, fmt.Errorf("node %d not connected", nodeID)
	}

	ch := make(chan *pb.NodeMessage, 64)
	conn.mu.Lock()
	if conn.pending == nil {
		conn.mu.Unlock()
		return nil, fmt.Errorf("node %d connection closed", nodeID)
	}
	conn.pending[msg.RequestId] = ch
	conn.mu.Unlock()

	// Send request
	if err := conn.Stream.Send(msg); err != nil {
		conn.mu.Lock()
		delete(conn.pending, msg.RequestId)
		conn.mu.Unlock()
		close(ch)
		return nil, fmt.Errorf("failed to send to node %d: %w", nodeID, err)
	}
	return ch, nil
}

// CleanupRequest removes a pending request channel (used after streaming is done).
func (r *Registry) CleanupRequest(nodeID int, requestID string) {
	conn, ok := r.GetConnection(nodeID)
	if !ok {
		return
	}
	conn.mu.Lock()
	delete(conn.pending, requestID)
	conn.mu.Unlock()
}

// RouteResponse delivers an incoming message to the waiting handler via request_id.
// Returns false if no handler is waiting (message is dropped).
func (conn *NodeConnection) RouteResponse(msg *pb.NodeMessage) bool {
	conn.mu.Lock()
	ch, ok := conn.pending[msg.RequestId]
	conn.mu.Unlock()

	if !ok || ch == nil {
		return false
	}

	select {
	case ch <- msg:
		return true
	default:
		return false
	}
}

// CloseStreamingRequest closes the channel for a specific request_id
// (called when TransferDone is received to signal end of chunked transfer).
func (conn *NodeConnection) CloseStreamingRequest(requestID string) {
	conn.mu.Lock()
	if ch, ok := conn.pending[requestID]; ok {
		close(ch)
		delete(conn.pending, requestID)
	}
	conn.mu.Unlock()
}
