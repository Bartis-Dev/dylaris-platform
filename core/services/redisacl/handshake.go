package redisacl

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"errors"
)

var (
	ErrEnrollInvalid = errors.New("redisacl: invalid or expired enroll token")
	ErrNodeLimit     = errors.New("redisacl: node limit reached for owner")
)

// HandshakeStore is the narrow data port the gRPC ACL handshake needs.
// Implemented by an adapter over the Core store in main.go. Primitive types
// only, so this package stays free of store/models imports. It is a superset of
// secretStore (Get/SetNodeSecretEnc), so it can be passed to LoadOrCreateNodeSecret.
type HandshakeStore interface {
	GetNodeSecretEnc(id int) (string, error)
	SetNodeSecretEnc(id int, enc string) error
	ServerUUIDsByNode(nodeID int) ([]string, error)
	ResolveEnrollToken(plaintext string) (ownerID string, ok bool, err error)
	ConsumeEnrollToken(plaintext string) (ownerID string, ok bool, err error)
	NodeLimitReached(ownerID string) bool
	CreateBYONNode(token, address, ownerID string) (id int, err error)
	NodeIDByToken(token string) (id int, found bool, err error)
}

// Handshake performs the gated ACL bootstrap during the node gRPC handshake.
type Handshake struct {
	store         HandshakeStore
	prov          *Provisioner
	clusterSecret string
	enabled       func(ctx context.Context) bool
}

func NewHandshake(store HandshakeStore, prov *Provisioner, clusterSecret string, enabled func(ctx context.Context) bool) *Handshake {
	return &Handshake{store: store, prov: prov, clusterSecret: clusterSecret, enabled: enabled}
}

func (h *Handshake) Enabled(ctx context.Context) bool { return h != nil && h.enabled(ctx) }

// ensure loads/mints the secret, provisions the ACL from the assigned servers,
// and returns the secret hex.
func (h *Handshake) ensure(ctx context.Context, nodeID int, token string) (string, error) {
	secret, err := LoadOrCreateNodeSecret(h.store, h.clusterSecret, nodeID)
	if err != nil {
		return "", err
	}
	uuids, err := h.store.ServerUUIDsByNode(nodeID)
	if err != nil {
		return "", err
	}
	if err := h.prov.EnsureNodeACL(ctx, token, secret, uuids); err != nil {
		return "", err
	}
	return hex.EncodeToString(secret), nil
}

// EnsureExisting provisions the ACL for an already-registered node and returns
// its secret hex.
func (h *Handshake) EnsureExisting(ctx context.Context, nodeID int, token string) (string, error) {
	return h.ensure(ctx, nodeID, token)
}

// EnsureForToken re-applies the ACL for the node identified by its token. A no-op
// (nil) for an unknown token — nothing to provision yet.
func (h *Handshake) EnsureForToken(ctx context.Context, token string) error {
	id, ok, err := h.store.NodeIDByToken(token)
	if err != nil || !ok {
		return err
	}
	_, err = h.ensure(ctx, id, token)
	return err
}

// Enroll creates a BYON node row bound to the enroll token's owner, then
// provisions its ACL. Returns the new node id + secret hex.
func (h *Handshake) Enroll(ctx context.Context, token, enrollToken, address string) (int, string, error) {
	ownerID, ok, err := h.store.ResolveEnrollToken(enrollToken)
	if err != nil {
		return 0, "", err
	}
	if !ok {
		return 0, "", ErrEnrollInvalid
	}
	if h.store.NodeLimitReached(ownerID) {
		return 0, "", ErrNodeLimit
	}
	// Single-use: atomically consume now. If a concurrent connect (or the
	// discovery path) already consumed it, this returns ok=false and we reject.
	consumedOwner, cok, cerr := h.store.ConsumeEnrollToken(enrollToken)
	if cerr != nil {
		return 0, "", cerr
	}
	if !cok {
		return 0, "", ErrEnrollInvalid
	}
	id, err := h.store.CreateBYONNode(token, address, consumedOwner)
	if err != nil {
		return 0, "", err
	}
	secretHex, err := h.ensure(ctx, id, token)
	return id, secretHex, err
}

// HasSecret reports whether a non-mint-able secret is already stored for the node.
func (h *Handshake) HasSecret(ctx context.Context, nodeID int) (bool, error) {
	_, ok, err := LoadNodeSecret(h.store, h.clusterSecret, nodeID)
	return ok, err
}

// VerifyProof loads the node's stored secret (no mint) and checks the proof.
func (h *Handshake) VerifyProof(ctx context.Context, nodeID int, token, proof string) (bool, error) {
	secret, ok, err := LoadNodeSecret(h.store, h.clusterSecret, nodeID)
	if err != nil || !ok {
		return false, err
	}
	return VerifyProof(secret, token, proof), nil
}

// VerifyChallenge loads the node's stored secret (no mint) and checks the
// response against the Core-issued nonce.
func (h *Handshake) VerifyChallenge(ctx context.Context, nodeID int, nonce, response string) (bool, error) {
	secret, ok, err := LoadNodeSecret(h.store, h.clusterSecret, nodeID)
	if err != nil || !ok {
		return false, err
	}
	return VerifyChallenge(secret, nonce, response), nil
}

// VerifyClusterProof checks a node's cluster_proof against CLUSTER_SECRET.
func (h *Handshake) VerifyClusterProof(token, proof string) bool {
	if proof == "" {
		return false
	}
	return hmac.Equal([]byte(ClusterProof(h.clusterSecret, token)), []byte(proof))
}
