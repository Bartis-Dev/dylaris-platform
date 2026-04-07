package nodegrpc

import (
	"fmt"
	"io"
	"log"
	"net"

	pb "dylaris-proto/node"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"time"
)

// NodeLookup interface for looking up Nodes by token.
// Implemented by the store.Store interface.
type NodeLookup interface {
	GetNodeByToken(token string) (*Node, error)
}

// Node is a minimal representation used by the gRPC layer.
// Matches the fields needed from models.Node.
type Node struct {
	ID    int
	Token string
}

// StoreAdapter wraps any store that has GetNodeByToken returning *models.Node.
type StoreAdapter struct {
	GetByToken func(token string) (id int, err error)
}

func (a *StoreAdapter) GetNodeByToken(token string) (*Node, error) {
	id, err := a.GetByToken(token)
	if err != nil {
		return nil, err
	}
	return &Node{ID: id, Token: token}, nil
}

// Server implements the NodeService gRPC server.
type Server struct {
	pb.UnimplementedNodeServiceServer
	registry    *Registry
	nodeLookup  NodeLookup
	coreID      string
}

// NewServer creates a new gRPC server for Node connections.
func NewServer(registry *Registry, lookup NodeLookup, coreID string) *Server {
	return &Server{
		registry:   registry,
		nodeLookup: lookup,
		coreID:     coreID,
	}
}

// NodeConnect handles a bidirectional stream from a Node.
// Flow: Node connects → sends NodeAuth → Core validates → stream stays open.
func (s *Server) NodeConnect(stream pb.NodeService_NodeConnectServer) error {
	// Step 1: Wait for auth message (first message must be NodeAuth)
	firstMsg, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("failed to receive auth: %w", err)
	}

	auth := firstMsg.GetAuth()
	if auth == nil {
		_ = stream.Send(&pb.NodeMessage{
			Payload: &pb.NodeMessage_AuthResult{
				AuthResult: &pb.AuthResult{Ok: false, Message: "first message must be NodeAuth"},
			},
		})
		return fmt.Errorf("first message was not NodeAuth")
	}

	// Step 2: Validate node token against DB
	node, err := s.nodeLookup.GetNodeByToken(auth.NodeToken)
	if err != nil {
		_ = stream.Send(&pb.NodeMessage{
			Payload: &pb.NodeMessage_AuthResult{
				AuthResult: &pb.AuthResult{Ok: false, Message: "invalid node token"},
			},
		})
		return fmt.Errorf("auth failed for token %s: %w", auth.NodeToken[:8], err)
	}

	// Step 3: Send auth result
	if err := stream.Send(&pb.NodeMessage{
		Payload: &pb.NodeMessage_AuthResult{
			AuthResult: &pb.AuthResult{Ok: true, CoreId: s.coreID},
		},
	}); err != nil {
		return fmt.Errorf("failed to send auth result: %w", err)
	}

	// Step 4: Register connection
	conn := s.registry.Register(node.ID, auth.NodeToken, stream)
	log.Printf("gRPC: Node %d connected (token=%s...)", node.ID, auth.NodeToken[:8])

	defer func() {
		s.registry.Unregister(node.ID)
		log.Printf("gRPC: Node %d disconnected", node.ID)
	}()

	// Step 5: Read loop — route incoming messages to waiting handlers
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("node %d stream error: %w", node.ID, err)
		}

		// Route response to the handler waiting on this request_id
		if !conn.RouteResponse(msg) {
			log.Printf("gRPC: Node %d sent unroutable message (request_id=%s)", node.ID, msg.RequestId)
		}

		// Close the streaming channel when the final TransferDone arrives.
		// Metadata TransferDone has Filename set and TotalBytes==0 (sent before chunks).
		// Final TransferDone has no Filename (TotalBytes>0 for non-empty, ==0 for empty files).
		if done := msg.GetTransferDone(); done != nil && (done.TotalBytes > 0 || done.Filename == "") {
			conn.CloseStreamingRequest(msg.RequestId)
		}
	}
}

// StartGRPCServer starts the gRPC server on the given port.
// Blocks until the server is stopped.
func StartGRPCServer(port int, registry *Registry, lookup NodeLookup, coreID string) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	grpcServer := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.MaxRecvMsgSize(128*1024), // 128KB max message (64KB chunks + overhead)
	)

	srv := NewServer(registry, lookup, coreID)
	pb.RegisterNodeServiceServer(grpcServer, srv)

	log.Printf("gRPC: NodeService listening on :%d", port)
	return grpcServer.Serve(lis)
}
