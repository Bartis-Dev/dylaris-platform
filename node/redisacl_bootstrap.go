package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	pb "dylaris-proto/node"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

// nodeSecret is the node's per-node Redis secret once obtained. Read by
// grpc_mesh (proof) and docker_mgr (shipper creds). nil until ensureNodeSecret
// completes the bootstrap.
var nodeSecret []byte

// linkSecret / linkDiscoveryProof are the Core-delivered Link tunnel credentials
// (empty when this node isn't gateway-relevant, or until ensureNodeSecret
// completes the bootstrap). Read by the link reconciler (docker_mgr) to spawn
// the Link sidecar.
var (
	linkSecret         string
	linkDiscoveryProof string
)

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
		linkSecret, linkDiscoveryProof = s, p
	}
	if s, ok := loadNodeSecret(nodeSecretDir); ok {
		nodeSecret = s
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
			if werr := saveNodeSecret(nodeSecretDir, s); werr != nil {
				log.Printf("redisacl: WARN failed to persist node secret: %v", werr)
			}
			nodeSecret = s
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
		linkSecret, linkDiscoveryProof = res.LinkSecret, res.LinkDiscoveryProof
		if werr := saveLinkCreds(nodeSecretDir, res.LinkSecret, res.LinkDiscoveryProof); werr != nil {
			log.Printf("redisacl: WARN failed to persist link creds: %v", werr)
		}
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
		prev := nodeSecret
		if s, berr := bootstrapSecretViaGRPC(ctx); berr == nil && len(s) == 32 {
			_ = saveNodeSecret(nodeSecretDir, s)
			nodeSecret = s
			if prev != nil && !bytes.Equal(prev, s) {
				// Core minted a DIFFERENT per-node secret (a genuine pairing reset).
				// The shared rdb was built with the OLD password and cannot be swapped
				// in place from this goroutine, so restart the agent: the proven
				// startup path rebuilds rdb from the now-cached new secret. Uses the
				// module's fatal-to-restart idiom (log.Fatal, as in main.go). The MC
				// server containers are separate and unaffected; only the node
				// management plane briefly blips. plain bytes.Equal is fine here: this
				// is a self-owned value, not attacker input.
				log.Fatal("redisacl: per-node secret rotated; restarting node agent to adopt new Redis credentials")
			}
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
