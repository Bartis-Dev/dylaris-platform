package services

import (
	"context"
	"encoding/json"

	"dylaris-pkg/storageplacement"

	"github.com/redis/go-redis/v9"
)

// FleetPlacementKey holds the placement policy a node uses when it has none of
// its own. It sits under dylaris:placement: because that prefix is already in
// every node's read ACL.
//
// A fleet-wide ORDER is deliberately more useful than a single "default path"
// would be: OrderPaths already drops entries a node does not have and appends
// the ones it was not told about, so one list of ["/storage"] expresses
// "/storage first everywhere, anything else after it" across a mixed fleet
// without naming a single node.
const FleetPlacementKey = "dylaris:placement:storage_default"

// LoadFleetPlacement reads the fleet default. Missing or unparseable means auto,
// the same fallback a node applies on its own.
func LoadFleetPlacement(ctx context.Context, rdb *redis.Client) NodePlacement {
	out := NodePlacement{Mode: storageplacement.ModeAuto}
	if rdb == nil {
		return out
	}
	val, err := rdb.Get(ctx, FleetPlacementKey).Result()
	if err != nil || val == "" {
		return out
	}
	var stored NodePlacement
	if json.Unmarshal([]byte(val), &stored) != nil {
		return out
	}
	stored.Mode = storageplacement.NormalizeMode(stored.Mode)
	return stored
}

// SaveFleetPlacement writes the fleet default. No TTL: it is configuration, and
// nodes re-read it on every settings poll.
func SaveFleetPlacement(ctx context.Context, rdb *redis.Client, p NodePlacement) error {
	if rdb == nil {
		return nil
	}
	p.Mode = storageplacement.NormalizeMode(p.Mode)
	if p.Order == nil {
		p.Order = []string{}
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, FleetPlacementKey, payload, 0).Err()
}

// EffectiveNodePlacement is the policy a node actually applies: its own if it
// has one, otherwise the fleet default. Mirrors the node's own resolution so
// Core's capacity prediction stays truthful.
//
// "Has one" means a manual mode or a non-empty order. A node stored as plain
// auto with no order carries no information, so it falls through to the fleet
// default rather than pinning the node to auto forever.
func EffectiveNodePlacement(ctx context.Context, rdb *redis.Client, nodeToken string) NodePlacement {
	own := LoadNodePlacement(ctx, rdb, nodeToken)
	if own.Mode == storageplacement.ModeManual || len(own.Order) > 0 {
		return own
	}
	return LoadFleetPlacement(ctx, rdb)
}

// FleetStoragePaths is every storage path any online node reports, deduplicated
// and in first-seen order. It is what the fleet-default editor offers, since no
// single node's path list describes the fleet.
func FleetStoragePaths(ctx context.Context, rdb *redis.Client) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, hb := range LoadHeartbeats(ctx, rdb) {
		for _, sp := range hb.Storage {
			if sp.Path == "" || seen[sp.Path] {
				continue
			}
			seen[sp.Path] = true
			out = append(out, sp.Path)
		}
	}
	return out
}
