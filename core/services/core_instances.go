package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/redis/go-redis/v9"
)

// This is the READ side of the heartbeats CoreHeartbeatService writes. Until
// now the only reader lived in the node agent (node/grpc_mesh.go scanCores),
// so nothing under core/ could answer "how many Cores are online?" - which is
// the question the host-path storage backend depends on, because a host path
// is only correct on a single Core.
//
// Deliberately NOT built on the telemetry instance id: that value is
// per-DEPLOYMENT by design (a hash of CLUSTER_SECRET, identical on every
// replica) and is leader-gated on top, so it is architecturally incapable of
// counting instances.

// coreLeaderKey shares the dylaris:core: prefix with the heartbeats but is the
// leader lease, not an instance registry - it holds the current leader's id as
// a bare string. Counting it would report two Cores on a single-Core install.
// It is excluded by name AND, as a second line of defence, by the requirement
// below that a value parse as a heartbeat with a non-empty id.
const coreLeaderKey = "dylaris:core:leader"

// coreScanKeyBudget bounds the SCAN loop. The keyspace being walked is written
// only by Cores with a 30s TTL, so anything past this is a corrupted or
// hostile keyspace rather than a real cluster; stopping is better than letting
// one HTTP request walk an unbounded keyspace.
const coreScanKeyBudget = 10000

// OnlineCoreIDs returns the ids of the Core instances currently heartbeating,
// sorted, deduplicated, at most coreScanKeyBudget keys examined.
//
// SCAN rather than KEYS (which node/grpc_mesh.go uses): KEYS blocks the whole
// Redis server for the length of the walk, and this runs on an HTTP request
// path shared with every other subsystem on the same Redis.
//
// A count of 0 is possible and is NOT an error: the caller's own heartbeat may
// not have landed yet, or may have failed to write. Callers must treat 0 and 1
// alike - "not more than one" - and never read 0 as "no Cores exist", because
// the process asking is itself a Core.
//
// Two known ways the count is not the literal number of running processes, both
// of which a caller that gates on it has to live with:
//   - It can UNDERCOUNT. Instances are distinguished by DYLARIS_CORE_ID, which
//     defaults to the hostname. Two Cores configured with the same id write the
//     same key and are indistinguishable here.
//   - It can OVERCOUNT for up to 30s (the heartbeat TTL) after a Core stops
//     WITHOUT running its shutdown path - SIGKILL, OOM, host failure - because
//     the key then expires rather than being deleted. A Core that shuts down
//     cleanly deletes its own key (CoreHeartbeatService.Stop), so an orderly
//     rolling restart no longer shows the outgoing instance.
func OnlineCoreIDs(ctx context.Context, rdb *redis.Client) ([]string, error) {
	if rdb == nil {
		return nil, fmt.Errorf("core instances: no redis client")
	}

	seen := make(map[string]struct{})
	var cursor uint64
	examined := 0

	for {
		keys, next, err := rdb.Scan(ctx, cursor, "dylaris:core:*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("core instances: scan: %w", err)
		}

		for _, key := range keys {
			examined++
			if key == coreLeaderKey {
				continue
			}

			val, err := rdb.Get(ctx, key).Result()
			if err != nil {
				// A key that expired between the SCAN and this GET is the
				// normal race, not a failure: the heartbeat it belonged to is
				// stale by definition, so skipping it is the right answer.
				continue
			}

			var hb CoreHeartbeat
			if err := json.Unmarshal([]byte(val), &hb); err != nil {
				continue
			}
			if hb.ID == "" {
				continue
			}
			// Keyed on the id inside the payload rather than the key name, so
			// a stray key holding a duplicate heartbeat cannot inflate the
			// count past the number of distinct instances.
			seen[hb.ID] = struct{}{}
		}

		cursor = next
		if cursor == 0 || examined >= coreScanKeyBudget {
			break
		}
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
