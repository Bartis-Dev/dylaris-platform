package services

import (
	"context"
	"encoding/json"

	"dylaris-pkg/protocol"
)

// gatewayCapacity is the F0 telemetry view F1 needs: which swarm host each warp
// leader runs on (from its component mirror) and each host's free transmit
// capacity in bits/s (from its host aggregate). Both maps omit any entry with no
// usable telemetry; a missing key means "capacity unknown", which the callers
// treat as a fall-back to the historical deterministic order.
type gatewayCapacity struct {
	leaderHost map[string]string // leaderID -> swarm host
	hostFree   map[string]int64  // host -> free bits/s (budget - tx, clamped >= 0)
}

// freeBpsForLeader returns the free capacity of the host a leader runs on and
// whether that capacity is known. Reading a nil map is safe (zero, false), so a
// zero-value gatewayCapacity reports every leader as unknown.
func (c gatewayCapacity) freeBpsForLeader(leaderID string) (int64, bool) {
	host, ok := c.leaderHost[leaderID]
	if !ok {
		return 0, false
	}
	free, ok := c.hostFree[host]
	return free, ok
}

// hostFreeBps converts a host aggregate into free transmit capacity in bits/s.
// The budget is BudgetMbit megabit/s (1 Mbit = 1e6 bits); a host with no
// configured budget (BudgetMbit <= 0, i.e. BANDWIDTH_MBIT unset) has UNKNOWN
// free capacity, not zero. Free is clamped at 0 so an over-budget host never
// reports negative headroom.
func hostFreeBps(agg hostAggregate) (int64, bool) {
	if agg.BudgetMbit <= 0 {
		return 0, false
	}
	free := int64(agg.BudgetMbit)*1_000_000 - int64(agg.TxBps)
	if free < 0 {
		free = 0
	}
	return free, true
}

// loadGatewayCapacity reads the F0 Redis mirror for the given warp leaders and
// builds the capacity view. It does two MGETs: the per-leader component mirrors
// to learn each leader's host, then the per-host aggregates to learn each host's
// free capacity. Any leader or host with a missing or unparseable mirror is
// simply absent from the result (= unknown). Best-effort: a Redis error yields
// an empty view, never an error, so placement degrades to the deterministic
// fallback rather than failing enroll.
func (s *WarpService) loadGatewayCapacity(ctx context.Context, leaderIDs []string) gatewayCapacity {
	gc := gatewayCapacity{
		leaderHost: map[string]string{},
		hostFree:   map[string]int64{},
	}
	if len(leaderIDs) == 0 || s.redis == nil {
		return gc
	}

	compKeys := make([]string, len(leaderIDs))
	for i, id := range leaderIDs {
		compKeys[i] = "dylaris:gwbw:component:warp:" + id
	}
	compVals, err := s.redis.MGet(ctx, compKeys...).Result()
	if err != nil {
		return gc
	}
	hostSet := map[string]bool{}
	for i, v := range compVals {
		raw, ok := v.(string)
		if !ok {
			continue
		}
		var stat protocol.GatewayStats
		if json.Unmarshal([]byte(raw), &stat) != nil || stat.Host == "" {
			continue
		}
		gc.leaderHost[leaderIDs[i]] = stat.Host
		hostSet[stat.Host] = true
	}
	if len(hostSet) == 0 {
		return gc
	}

	hosts := make([]string, 0, len(hostSet))
	hostKeys := make([]string, 0, len(hostSet))
	for h := range hostSet {
		hosts = append(hosts, h)
		hostKeys = append(hostKeys, "dylaris:gwbw:host:"+h)
	}
	hostVals, err := s.redis.MGet(ctx, hostKeys...).Result()
	if err != nil {
		return gc
	}
	for i, v := range hostVals {
		raw, ok := v.(string)
		if !ok {
			continue
		}
		var agg hostAggregate
		if json.Unmarshal([]byte(raw), &agg) != nil {
			continue
		}
		if free, known := hostFreeBps(agg); known {
			gc.hostFree[hosts[i]] = free
		}
	}
	return gc
}
