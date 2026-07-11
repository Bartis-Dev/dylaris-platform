package redisacl

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Provisioner applies per-node ACLs using an admin (default-user) Redis client.
type Provisioner struct {
	admin *redis.Client // Core's existing client (default user = admin)
}

func NewProvisioner(admin *redis.Client) *Provisioner { return &Provisioner{admin: admin} }

// EnsureNodeACLNoSave (idempotent) sets the node, shipper, and link users from
// the authoritative server list WITHOUT persisting (no ACL SAVE). secret is the
// node's 32-byte per-node secret. The caller issues a single trailing SaveACL so
// a full reconcile sweep does one temp-file+rename, not one per node.
func (p *Provisioner) EnsureNodeACLNoSave(ctx context.Context, token, tunnelToken string, secret []byte, serverUUIDs []string) error {
	nodePw := NodePassword(secret, token)
	shipPw := ShipperPassword(secret, token)
	linkPw := LinkPassword(secret, token)

	nodeArgs := SetUserArgs(NodeUsername(token), BuildNodeACLRules(token, nodePw, serverUUIDs))
	if err := p.admin.Do(ctx, nodeArgs...).Err(); err != nil {
		return err
	}
	shipArgs := SetUserArgs(ShipperUsername(token), BuildShipperACLRules(shipPw, serverUUIDs))
	if err := p.admin.Do(ctx, shipArgs...).Err(); err != nil {
		return err
	}
	linkArgs := SetUserArgs(LinkUsername(token), BuildLinkACLRules(linkPw, token, tunnelToken))
	if err := p.admin.Do(ctx, linkArgs...).Err(); err != nil {
		return err
	}
	return nil
}

// EnsureNodeACL (idempotent) applies the node's scoped users then persists via
// ACL SAVE (best-effort, loud on failure). Safe to call on every connect and
// before every placement.
func (p *Provisioner) EnsureNodeACL(ctx context.Context, token, tunnelToken string, secret []byte, serverUUIDs []string) error {
	if err := p.EnsureNodeACLNoSave(ctx, token, tunnelToken, secret, serverUUIDs); err != nil {
		return err
	}
	// Persist so a Valkey restart (even while Core is offline) keeps these users.
	p.SaveACL(ctx)
	return nil
}

// RemoveNodeACL drops all three users (e.g. node deleted). Best-effort.
func (p *Provisioner) RemoveNodeACL(ctx context.Context, token string) {
	_ = p.admin.Do(ctx, "ACL", "DELUSER", NodeUsername(token)).Err()
	_ = p.admin.Do(ctx, "ACL", "DELUSER", ShipperUsername(token)).Err()
	_ = p.admin.Do(ctx, "ACL", "DELUSER", LinkUsername(token)).Err()
	p.SaveACL(ctx)
}

// EnsureRouteOnlyLinkACLNoSave applies the route-only link ACL user WITHOUT
// persisting (no ACL SAVE). instanceID is tunnelToken[:8], matching the link's
// own errlog instance id (gateway/link/link.go). Returns the derived ACL
// username + password; the caller issues the trailing SaveACL.
func (p *Provisioner) EnsureRouteOnlyLinkACLNoSave(ctx context.Context, clusterSecret, linkID, tunnelToken string) (user, pass string, err error) {
	user = RouteOnlyLinkUsername(linkID)
	pass = RouteOnlyLinkPassword(clusterSecret, linkID)
	instanceID := tunnelToken[:8]
	args := SetUserArgs(user, BuildRouteOnlyLinkACLRules(pass, tunnelToken, instanceID))
	if err = p.admin.Do(ctx, args...).Err(); err != nil {
		return "", "", err
	}
	return user, pass, nil
}

// EnsureRouteOnlyLinkACL is idempotent: it recomputes the derived password,
// re-applies the rule set, then persists (best-effort, loud on failure). Returns
// the ACL username + password so the boot endpoint can hand them to the link.
func (p *Provisioner) EnsureRouteOnlyLinkACL(ctx context.Context, clusterSecret, linkID, tunnelToken string) (user, pass string, err error) {
	user, pass, err = p.EnsureRouteOnlyLinkACLNoSave(ctx, clusterSecret, linkID, tunnelToken)
	if err != nil {
		return "", "", err
	}
	p.SaveACL(ctx)
	return user, pass, nil
}

// RemoveRouteOnlyLinkACLNoSave drops the user WITHOUT persisting (no ACL SAVE),
// terminating its live Redis connections (ACL DELUSER). The DELUSER error is
// logged loudly rather than silently discarded (matching SaveACL/SelfProbe's
// style) but is never returned: the caller's own teardown (revoke, suspend, or
// the reconciler's cleanup sweep) must not be blocked by a transient Redis
// blip. DELUSER is idempotent (no error on an absent user), so a retry - e.g.
// the reconciler's next tick - is always safe. Callers tearing down a single
// link in isolation should use RemoveRouteOnlyLinkACL below; a sweep tearing
// down several issues one trailing SaveACL itself, exactly like
// EnsureRouteOnlyLinkACLNoSave does for the ensure side.
func (p *Provisioner) RemoveRouteOnlyLinkACLNoSave(ctx context.Context, linkID string) {
	if err := p.admin.Do(ctx, "ACL", "DELUSER", RouteOnlyLinkUsername(linkID)).Err(); err != nil {
		log.Printf("redisacl: WARNING: ACL DELUSER failed for route-only link %s: %v. The scoped Redis user may still be live; retrying on the next reconcile sweep.", linkID, err)
	}
}

// RemoveRouteOnlyLinkACL drops the user, terminating its live Redis connections
// (ACL DELUSER), then persists. Best-effort.
func (p *Provisioner) RemoveRouteOnlyLinkACL(ctx context.Context, linkID string) {
	p.RemoveRouteOnlyLinkACLNoSave(ctx, linkID)
	p.SaveACL(ctx)
}

// aclSaveWarnEvery throttles the loud ACL SAVE failure warning so a persistently
// broken aclfile does not flood the log on every provision and reconcile tick.
const aclSaveWarnEvery = 5 * time.Minute

var (
	aclSaveWarnMu   sync.Mutex
	aclSaveWarnLast time.Time
)

// SaveACL persists the in-memory ACL users to the aclfile (ACL SAVE), best-effort.
// On failure it emits a loud, throttled WARN naming the consequence. It never
// returns an error: persistence is best-effort by design and the in-memory ACL
// still enforces; recovery no longer depends on it (the reconciler re-provisions).
func (p *Provisioner) SaveACL(ctx context.Context) {
	err := p.admin.Do(ctx, "ACL", "SAVE").Err()
	if err == nil {
		return
	}
	aclSaveWarnMu.Lock()
	defer aclSaveWarnMu.Unlock()
	if time.Since(aclSaveWarnLast) < aclSaveWarnEvery {
		return
	}
	aclSaveWarnLast = time.Now()
	log.Printf("redisacl: WARNING: ACL SAVE failed: %v. Scoped Redis users are IN-MEMORY ONLY and will be LOST on a Valkey restart until the next reconcile re-provisions them. Ensure Valkey runs with --aclfile on a writable, persisted volume.", err)
}

// SelfProbe runs once at boot to surface a broken ACL-persistence setup early. It
// issues ACL SAVE and, on failure, logs a loud one-time boot warning so an
// operator learns the aclfile is not persisting now, not at the first Valkey
// restart. A passing probe means scoped users survive a restart.
func (p *Provisioner) SelfProbe(ctx context.Context) {
	if err := p.admin.Do(ctx, "ACL", "SAVE").Err(); err != nil {
		log.Printf("redisacl: BOOT WARNING: ACL persistence is NOT working (ACL SAVE failed: %v). Scoped Redis users are IN-MEMORY ONLY; a Valkey restart drops them until the reconciler re-provisions (up to one interval later). Configure Valkey with --aclfile on a writable, persisted volume.", err)
		return
	}
	log.Println("redisacl: ACL persistence verified (ACL SAVE ok)")
}
