package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	pb "dylaris-proto/node"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

// nodeSecret is the node's per-node Redis secret once obtained. nil until
// ensureNodeSecret completes the bootstrap. Written from three independent
// call sites - main's startup path, the ACL watchdog's re-bootstrap, and the
// gRPC mesh's per-Core-connection reconnect handler - so every read and write
// goes through the guarded accessors below (getNodeSecret/setNodeSecret),
// never this global directly.
var nodeSecret []byte

// linkSecret / linkDiscoveryProof are the Core-delivered Link tunnel credentials
// (empty when this node isn't gateway-relevant, or until ensureNodeSecret
// completes the bootstrap). Same multi-writer concern as nodeSecret: go
// through getLinkCreds/setLinkCreds, never these globals directly.
var (
	linkSecret         string
	linkDiscoveryProof string
)

// nodeSecretMu guards nodeSecret, linkSecret and linkDiscoveryProof. Before
// this existed, grpc_mesh's per-reconnect handler wrote nodeSecret directly
// with no synchronization AND with no change-detection, which could blind the
// ACL watchdog: the watchdog's own "prev := nodeSecret" read would observe
// the already-updated value, bytes.Equal would be true, no restart would
// fire, and the node would be stranded on a stale rdb password until a
// manual restart. Every writer now goes through setNodeSecret, which applies
// the SAME change-detection + restart rule regardless of caller.
var nodeSecretMu sync.Mutex

// getNodeSecret returns the current per-node secret (nil until the startup
// bootstrap completes). Safe for concurrent use.
func getNodeSecret() []byte {
	nodeSecretMu.Lock()
	defer nodeSecretMu.Unlock()
	return nodeSecret
}

// getLinkCreds returns the current Link tunnel token + discovery proof. Safe
// for concurrent use.
func getLinkCreds() (secret, proof string) {
	nodeSecretMu.Lock()
	defer nodeSecretMu.Unlock()
	return linkSecret, linkDiscoveryProof
}

// setNodeSecret installs a freshly obtained per-node secret. Applies the SAME
// rule no matter which caller triggers it: the FIRST install (prev is nil,
// e.g. the startup bootstrap or a cache load) never restarts; a genuine
// CHANGE from an already-loaded secret (a real pairing rotation, whether
// detected by the watchdog or by the gRPC mesh's reconnect handler) always
// logs loud and log.Fatal's, so the proven startup path rebuilds rdb from the
// new secret on restart. A re-confirmation of the SAME secret is a no-op
// restart-wise. persist=false skips the disk write for a caller that already
// has the value on disk (e.g. loading the cache at startup); every other
// caller passes true.
func setNodeSecret(s []byte, persist bool) {
	nodeSecretMu.Lock()
	prev := nodeSecret
	nodeSecret = s
	nodeSecretMu.Unlock()
	if persist {
		if werr := saveNodeSecret(nodeSecretDir, s); werr != nil {
			log.Printf("redisacl: WARN failed to persist node secret: %v", werr)
		}
	}
	if prev != nil && !bytes.Equal(prev, s) {
		// MC server containers are separate Docker containers and are
		// unaffected by this process restarting; only the node management
		// plane briefly blips.
		log.Fatal("redisacl: per-node secret rotated; restarting node agent to adopt new Redis credentials")
	}
}

// setLinkCreds installs freshly delivered Link tunnel credentials. No
// change-detection/restart logic: unlike nodeSecret, a stale Link credential
// does not leave any long-lived client with a wrong password baked in - the
// link reconciler (link_reconciler.go) re-reads via getLinkCreds on its next
// 30s tick and respawns the sidecar if the signature changed.
func setLinkCreds(secret, proof string, persist bool) {
	nodeSecretMu.Lock()
	linkSecret, linkDiscoveryProof = secret, proof
	nodeSecretMu.Unlock()
	if persist {
		if werr := saveLinkCreds(nodeSecretDir, secret, proof); werr != nil {
			log.Printf("redisacl: WARN failed to persist link creds: %v", werr)
		}
	}
}

// ensureNodeSecret returns the per-node secret. Uses the
// cached .node_secret when present (no Core contact needed — resilience); else
// bootstraps it from Core via a one-shot gRPC handshake. Loops until success or
// ctx cancel; never fatal.
func ensureNodeSecret(ctx context.Context) []byte {
	// Adopt a previously assigned identity before any credential/key derivation.
	hadAssignedID := false
	if id, ok := loadNodeID(nodeSecretDir); ok {
		nodeID = id
		hadAssignedID = true
	}
	if s, p, ok := loadLinkCreds(nodeSecretDir); ok {
		setLinkCreds(s, p, false) // already on disk, no need to re-persist
	}
	if s, ok := loadNodeSecret(nodeSecretDir); ok {
		setNodeSecret(s, false) // already on disk; first install, never restarts
		log.Println("redisacl: using cached node secret")
		return s
	}
	// P0b-5 hard guard: a node that already holds a server-assigned identity but has
	// no cached secret must NOT silently re-pair as if it were new. It has to prove
	// authority to re-pair. Accepted bootstrap credentials: a recovery token
	// (NODE_RECOVERY_TOKEN), an enroll token (NODE_ENROLL_TOKEN), or — for platform
	// nodes not yet migrated off CLUSTER_SECRET — the cluster secret (cluster proof).
	// Without any of these, fail loudly instead of spinning on a rejected handshake.
	if hadAssignedID && nodeRecoveryToken == "" && nodeEnrollToken == "" && clusterSecret == "" {
		log.Printf("redisacl: FATAL paired node id %s has no cached secret and no way to re-pair. "+
			"Obtain a recovery token from the panel (Settings -> Nodes -> Reset pairing) and start the node "+
			"with NODE_RECOVERY_TOKEN set to re-pair under this identity.", nodeID)
		return nil // caller (main.go) log.Fatal's on a nil secret -> process stops.
	}
	backoff := time.Second
	for {
		s, err := bootstrapSecretViaGRPC(ctx)
		if err == nil && len(s) == 32 {
			setNodeSecret(s, true) // first install (nodeSecret was nil until now), never restarts
			log.Println("redisacl: obtained node secret via gRPC bootstrap")
			return s
		}
		log.Printf("redisacl: secret bootstrap failed (retry in %s): %v", backoff, err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// bootstrapSecretViaGRPC does a one-shot NodeConnect to CORE_GRPC_ADDR. With a
// cached secret it sends a proof (Core re-applies the ACL, returns no new secret)
// and we return the cached secret; without one it sends the enroll token and Core
// mints + returns a fresh secret. Always contacts Core (used for first boot AND
// for re-confirm after a Redis auth failure).
func bootstrapSecretViaGRPC(ctx context.Context) ([]byte, error) {
	if coreGRPCAddr == "" {
		return nil, fmt.Errorf("CORE_GRPC_ADDR not set")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(coreGRPCAddr, coreDialCreds())
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	stream, err := pb.NewNodeServiceClient(conn).NodeConnect(dialCtx)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}

	auth := &pb.NodeAuth{NodeToken: nodeID, AclSupported: true}
	cached, hasCached := loadNodeSecret(nodeSecretDir)
	if hasCached {
		auth.SecretProof = aclProof(cached, nodeID)
	} else if nodeRecoveryToken != "" {
		// Re-pair under the existing identity via an admin-minted recovery token.
		auth.EnrollToken = nodeRecoveryToken
	} else if nodeEnrollToken != "" {
		auth.EnrollToken = nodeEnrollToken
	}
	if clusterSecret != "" {
		auth.ClusterProof = aclClusterProof(clusterSecret, nodeID)
	}
	ips := getNodeIPs()
	auth.Ips = &pb.NodeIPs{Public: ips.Public, Private: ips.Private}

	if err := stream.Send(&pb.NodeMessage{Payload: &pb.NodeMessage_Auth{Auth: auth}}); err != nil {
		return nil, fmt.Errorf("send auth: %w", err)
	}
	res, err := recvAuthResult(stream, cached)
	if err != nil {
		return nil, fmt.Errorf("recv auth result: %w", err)
	}
	if res == nil || !res.Ok {
		msg := "rejected"
		if res != nil {
			msg = res.Message
		}
		return nil, fmt.Errorf("auth rejected: %s", msg)
	}
	if res.AssignedId != "" && res.AssignedId != nodeID {
		nodeID = res.AssignedId
		if werr := saveNodeID(nodeSecretDir, res.AssignedId); werr != nil {
			log.Printf("redisacl: WARN failed to persist assigned node id: %v", werr)
		}
		log.Println("redisacl: adopted server-assigned node identity")
	}
	if res.LinkSecret != "" && res.LinkDiscoveryProof != "" {
		setLinkCreds(res.LinkSecret, res.LinkDiscoveryProof, true)
	}
	if res.NodeSecret != "" {
		raw, derr := hex.DecodeString(res.NodeSecret)
		if derr != nil || len(raw) != 32 {
			return nil, fmt.Errorf("bad node secret in auth result")
		}
		return raw, nil
	}
	if hasCached {
		return cached, nil // Core re-applied ACL for our existing secret
	}
	return nil, fmt.Errorf("no secret returned and none cached")
}

// redisACLWatchdog re-bootstraps the node's ACL over gRPC when Redis auth is
// sustainedly failing (NOAUTH/NOPERM/WRONGPASS), e.g. after a Valkey restart that
// dropped the aclfile. It does NOT rebuild rdb: the per-node secret is cached on
// disk and stable, so bootstrapSecretViaGRPC sends a proof, Core re-provisions the
// identical user + password, and the existing client re-auths transparently on its
// next command. This also breaks the mesh discovery chicken-and-egg (Cores are read
// from a now-authenticated Redis). Backoff-capped, throttled logs, never falls open.
func redisACLWatchdog(ctx context.Context, rdb *redis.Client) {
	const (
		probeEvery = 15 * time.Second
		failsToAct = 2 // consecutive auth failures before acting (~30s)
		maxBackoff = 5 * time.Minute
		logEvery   = 2 * time.Minute
	)
	backoff := probeEvery
	consecutive := 0
	var lastLog time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		err := rdb.Ping(ctx).Err()
		if err == nil {
			consecutive = 0
			backoff = probeEvery
			continue
		}
		if !isRedisAuthError(err) {
			// Redis unreachable (not an auth problem): a re-bootstrap would not
			// help and Core may be down too. Keep probing at the base interval.
			consecutive = 0
			backoff = probeEvery
			continue
		}
		consecutive++
		if time.Since(lastLog) >= logEvery {
			lastLog = time.Now()
			log.Printf("redisacl: WARNING sustained Redis auth failure (%v); re-bootstrapping ACL with Core", err)
		}
		if consecutive < failsToAct {
			continue
		}
		if s, berr := bootstrapSecretViaGRPC(ctx); berr == nil && len(s) == 32 {
			// setNodeSecret applies the change-detection + restart rule itself
			// (log.Fatal if this differs from the currently loaded secret), so
			// the lines below only run when the secret is unchanged (Core just
			// re-applied the same ACL after Valkey lost its aclfile).
			setNodeSecret(s, true)
			consecutive = 0
			backoff = probeEvery
			log.Println("redisacl: re-bootstrap OK; Core re-applied the node ACL")
		} else if berr != nil {
			// Core unreachable: stay fail-closed on Redis, back off and retry.
			if backoff < maxBackoff {
				backoff *= 2
			}
			log.Printf("redisacl: re-bootstrap failed (retry in %s): %v", backoff, berr)
		}
	}
}

// isRedisAuthError reports whether a Redis error is an ACL/auth rejection
// (NOAUTH/NOPERM/WRONGPASS) rather than a connectivity failure.
func isRedisAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "NOAUTH") || strings.Contains(s, "NOPERM") || strings.Contains(s, "WRONGPASS")
}
