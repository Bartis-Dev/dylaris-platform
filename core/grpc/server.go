package nodegrpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"

	beamauth "dylaris-pkg/beam/auth"
	pb "dylaris-proto/node"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"time"
)

// NodeLookup interface for looking up Nodes by token.
// Implemented by the store.Store interface.
type NodeLookup interface {
	GetNodeByToken(token string) (*Node, error)
}

// ACLHandshake is the optional per-node Redis-ACL bootstrap. nil = feature off.
// Implemented by *redisacl.Handshake, wired from main.
type ACLHandshake interface {
	Enabled(ctx context.Context) bool
	EnsureExisting(ctx context.Context, nodeID int, token string) (secretHex string, err error)
	Enroll(ctx context.Context, token, enrollToken, address string) (nodeID int, secretHex string, err error)
	VerifyProof(ctx context.Context, nodeID int, token, proof string) (ok bool, err error)
	VerifyChallenge(ctx context.Context, nodeID int, nonce, response string) (ok bool, err error)
	VerifyClusterProof(token, proof string) bool
	HasSecret(ctx context.Context, nodeID int) (ok bool, err error)
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
	registry   *Registry
	nodeLookup NodeLookup
	coreID     string
	acl        ACLHandshake
}

// NewServer creates a new gRPC server for Node connections.
func NewServer(registry *Registry, lookup NodeLookup, coreID string, acl ACLHandshake) *Server {
	return &Server{
		registry:   registry,
		nodeLookup: lookup,
		coreID:     coreID,
		acl:        acl,
	}
}

// NodeConnect handles a bidirectional stream from a Node.
// tokenPrefix returns up to the first 8 chars of a token for logging, without
// panicking on short/invalid tokens (e.g. a misconfigured node or a probe).
func tokenPrefix(t string) string {
	if len(t) > 8 {
		return t[:8]
	}
	return t
}

// newChallengeNonce returns a fresh 32-byte random nonce as lowercase hex for the
// per-connection reconnect challenge.
func newChallengeNonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

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

	// Step 2 + 3: Authenticate + send auth result.
	// When the ACL feature is on AND the node advertises support, the handshake
	// mints/provisions per-node Redis creds (and enrolls unknown BYON nodes).
	// Otherwise the OFF path below is byte-identical to the original handshake.
	ctx := stream.Context()
	sendFail := func(msg string) {
		_ = stream.Send(&pb.NodeMessage{Payload: &pb.NodeMessage_AuthResult{
			AuthResult: &pb.AuthResult{Ok: false, Message: msg},
		}})
	}

	featureOn := s.acl != nil && s.acl.Enabled(ctx)
	aclOn := featureOn && auth.AclSupported

	var node *Node
	if aclOn {
		address := ""
		if ips := auth.GetIps(); ips != nil {
			address = ips.GetPublic()
		}
		existing, lookErr := s.nodeLookup.GetNodeByToken(auth.NodeToken)
		if lookErr != nil {
			// Unknown node: enroll only with a valid enroll token.
			if auth.EnrollToken == "" {
				sendFail("unknown node and no enroll token")
				return fmt.Errorf("acl: unknown node %s without enroll token", tokenPrefix(auth.NodeToken))
			}
			id, secretHex, eerr := s.acl.Enroll(ctx, auth.NodeToken, auth.EnrollToken, address)
			if eerr != nil {
				sendFail("enrollment failed")
				return fmt.Errorf("acl: enroll failed for %s: %w", tokenPrefix(auth.NodeToken), eerr)
			}
			node = &Node{ID: id, Token: auth.NodeToken}
			if err := stream.Send(&pb.NodeMessage{Payload: &pb.NodeMessage_AuthResult{
				AuthResult: &pb.AuthResult{Ok: true, CoreId: s.coreID, AclEnabled: true, NodeSecret: secretHex},
			}}); err != nil {
				return fmt.Errorf("failed to send auth result: %w", err)
			}
		} else {
			node = existing
			hasSecret, serr := s.acl.HasSecret(ctx, node.ID)
			if serr != nil {
				sendFail("acl state error")
				return fmt.Errorf("acl: secret-state lookup failed for node %d: %w", node.ID, serr)
			}
			if hasSecret {
				// A provisioned node MUST prove possession of its secret against a
				// fresh single-use nonce, so a captured proof cannot be replayed. We
				// never re-hand the secret to a bare token holder; a node that
				// genuinely lost its cached secret recovers via operator action, not
				// a silent re-issue.
				nonce, nerr := newChallengeNonce()
				if nerr != nil {
					sendFail("challenge init failed")
					return fmt.Errorf("acl: nonce generation failed for node %d: %w", node.ID, nerr)
				}
				if err := stream.Send(&pb.NodeMessage{Payload: &pb.NodeMessage_Challenge{
					Challenge: &pb.NodeChallenge{Nonce: nonce},
				}}); err != nil {
					return fmt.Errorf("failed to send challenge: %w", err)
				}
				respMsg, rerr := stream.Recv()
				if rerr != nil {
					return fmt.Errorf("failed to receive challenge response: %w", rerr)
				}
				cr := respMsg.GetChallengeResponse()
				if cr == nil {
					sendFail("challenge response required")
					return fmt.Errorf("acl: node %d sent no challenge response", node.ID)
				}
				ok, verr := s.acl.VerifyChallenge(ctx, node.ID, nonce, cr.Response)
				if verr != nil || !ok {
					sendFail("bad challenge response")
					return fmt.Errorf("acl: bad challenge response for node %d", node.ID)
				}
			} else {
				// First issuance for a known node: require a cluster_proof (HMAC under
				// CLUSTER_SECRET). Closes the window where flipping feature_redis_acl on
				// (or a secret reset) would hand the secret to any bare-token holder.
				if !s.acl.VerifyClusterProof(node.Token, auth.ClusterProof) {
					sendFail("cluster proof required")
					return fmt.Errorf("acl: node %d first-issuance without valid cluster proof", node.ID)
				}
			}
			secretHex, perr := s.acl.EnsureExisting(ctx, node.ID, node.Token)
			if perr != nil {
				sendFail("acl provision failed")
				return fmt.Errorf("acl: provision failed for node %d: %w", node.ID, perr)
			}
			res := &pb.AuthResult{Ok: true, CoreId: s.coreID, AclEnabled: true}
			if !hasSecret {
				// First-time issue for this known node (feature newly enabled, or
				// the secret was reset). Deliver once; later connects must prove it.
				res.NodeSecret = secretHex
			}
			if err := stream.Send(&pb.NodeMessage{Payload: &pb.NodeMessage_AuthResult{AuthResult: res}}); err != nil {
				return fmt.Errorf("failed to send auth result: %w", err)
			}
		}
	} else {
		// OFF PATH: byte-identical to the original handshake when the feature is off.
		n, lookErr := s.nodeLookup.GetNodeByToken(auth.NodeToken)
		if lookErr != nil {
			sendFail("invalid node token")
			return fmt.Errorf("auth failed for token %s: %w", tokenPrefix(auth.NodeToken), lookErr)
		}
		// Downgrade guard: with feature_redis_acl on, a provisioned node (one that
		// has a per-node secret) MUST authenticate via the ACL challenge path.
		// Refuse a bare-token connection (AclSupported=false) for such a node so an
		// attacker cannot skip the possession proof by clearing AclSupported. Fail
		// closed on a lookup error, consistent with the ACL-path HasSecret check.
		if featureOn {
			has, herr := s.acl.HasSecret(ctx, n.ID)
			if herr != nil {
				sendFail("acl state error")
				return fmt.Errorf("acl: secret-state lookup failed for node %d: %w", n.ID, herr)
			}
			if has {
				sendFail("node must use ACL auth")
				return fmt.Errorf("acl: node %d has a secret but connected without ACL support", n.ID)
			}
		}
		node = n
		if err := stream.Send(&pb.NodeMessage{Payload: &pb.NodeMessage_AuthResult{
			AuthResult: &pb.AuthResult{Ok: true, CoreId: s.coreID},
		}}); err != nil {
			return fmt.Errorf("failed to send auth result: %w", err)
		}
	}

	// Step 4: Register connection
	conn := s.registry.Register(node.ID, auth.NodeToken, stream)
	log.Printf("gRPC: Node %d connected (token=%s...)", node.ID, tokenPrefix(auth.NodeToken))

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
// Blocks until the server is stopped. When tlsEnabled is set, it presents the
// cluster-wide certificate derived from clusterSecret (CLUSTER_SECRET) so nodes
// can pin its fingerprint; otherwise it serves plaintext (unchanged behavior).
func StartGRPCServer(port int, registry *Registry, lookup NodeLookup, coreID string, acl ACLHandshake, tlsEnabled bool, clusterSecret string) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.MaxRecvMsgSize(128*1024), // 128KB max message (64KB chunks + overhead)
	}

	if tlsEnabled {
		cert, fp, cerr := beamauth.DeriveClusterGRPCCert(clusterSecret)
		if cerr != nil {
			return fmt.Errorf("derive cluster gRPC cert: %w", cerr)
		}
		opts = append(opts, grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
		log.Printf("gRPC: NodeService TLS enabled (fingerprint pinning), cert fp=%s...", fp[:16])
	}

	grpcServer := grpc.NewServer(opts...)

	srv := NewServer(registry, lookup, coreID, acl)
	pb.RegisterNodeServiceServer(grpcServer, srv)

	log.Printf("gRPC: NodeService listening on :%d", port)
	return grpcServer.Serve(lis)
}
