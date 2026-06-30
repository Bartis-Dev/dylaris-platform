package redisacl

import (
	"context"
	"log"

	"github.com/redis/go-redis/v9"
)

// Provisioner applies per-node ACLs using an admin (default-user) Redis client.
type Provisioner struct {
	admin *redis.Client // Core's existing client (default user = admin)
}

func NewProvisioner(admin *redis.Client) *Provisioner { return &Provisioner{admin: admin} }

// EnsureNodeACL (idempotent) sets both the node user and the shipper user from
// the authoritative server list, then persists via ACL SAVE (best-effort).
// secret is the node's 32-byte per-node secret. Safe to call on every connect
// and before every placement.
func (p *Provisioner) EnsureNodeACL(ctx context.Context, token string, secret []byte, serverUUIDs []string) error {
	nodePw := NodePassword(secret, token)
	shipPw := ShipperPassword(secret, token)

	nodeArgs := SetUserArgs(NodeUsername(token), BuildNodeACLRules(token, nodePw, serverUUIDs))
	if err := p.admin.Do(ctx, nodeArgs...).Err(); err != nil {
		return err
	}
	shipArgs := SetUserArgs(ShipperUsername(token), BuildShipperACLRules(shipPw, serverUUIDs))
	if err := p.admin.Do(ctx, shipArgs...).Err(); err != nil {
		return err
	}
	// Persist so a Valkey restart (even while Core is offline) keeps these users.
	// Best-effort: with no aclfile configured this errors — warn loudly but do
	// NOT fail; the in-memory ACL still enforces until the next restart.
	if err := p.admin.Do(ctx, "ACL", "SAVE").Err(); err != nil {
		log.Printf("redisacl: ACL SAVE failed (aclfile configured?): %v — ACLs are in-memory only", err)
	}
	return nil
}

// RemoveNodeACL drops both users (e.g. node deleted). Best-effort.
func (p *Provisioner) RemoveNodeACL(ctx context.Context, token string) {
	_ = p.admin.Do(ctx, "ACL", "DELUSER", NodeUsername(token)).Err()
	_ = p.admin.Do(ctx, "ACL", "DELUSER", ShipperUsername(token)).Err()
	_ = p.admin.Do(ctx, "ACL", "SAVE").Err()
}
