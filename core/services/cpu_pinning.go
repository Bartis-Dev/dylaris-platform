package services

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"dylaris-core/store"

	"github.com/redis/go-redis/v9"
)

// CPUCore / CPUTopology mirror the JSON the node publishes to
// dylaris:node:{token}:cpu. Type is "P" | "E" | "standard".
type CPUCore struct {
	ID      int    `json:"id"`
	Type    string `json:"type"`
	Sibling int    `json:"sibling"`
}

type CPUTopology struct {
	LogicalCount  int       `json:"logicalCount"`
	PhysicalCount int       `json:"physicalCount"`
	Hybrid        bool      `json:"hybrid"`
	Cores         []CPUCore `json:"cores"`
	ScannedAt     int64     `json:"scannedAt"`
}

const cpuReserveHostCores = 1 // keep at least this many cores free for the host

// CPUPinningService reads node CPU topology from Redis and computes auto cpusets.
type CPUPinningService struct {
	redis *redis.Client
	store store.Store
}

func NewCPUPinningService(rdb *redis.Client, st store.Store) *CPUPinningService {
	return &CPUPinningService{redis: rdb, store: st}
}

// GetNodeTopology reads the topology a node published. Returns (nil, nil) when the
// node has not reported one (key missing) so callers can fall back gracefully.
func (s *CPUPinningService) GetNodeTopology(ctx context.Context, nodeToken string) (*CPUTopology, error) {
	raw, err := s.redis.Get(ctx, fmt.Sprintf("dylaris:node:%s:cpu", nodeToken)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var topo CPUTopology
	if err := json.Unmarshal([]byte(raw), &topo); err != nil {
		return nil, err
	}
	return &topo, nil
}

// NodeCoreLoad returns, per logical core id, how many servers on the node are
// currently pinned to it. excludeServerID is skipped (the server being changed).
func (s *CPUPinningService) NodeCoreLoad(nodeID, excludeServerID int) (map[int]int, error) {
	cpusets, err := s.store.ListServerCpusetsByNode(nodeID)
	if err != nil {
		return nil, err
	}
	load := map[int]int{}
	for serverID, cpuset := range cpusets {
		if serverID == excludeServerID {
			continue
		}
		for _, id := range parseCpusetList(cpuset) {
			load[id]++
		}
	}
	return load, nil
}

// AutoCpuset computes a spread, P-preferred cpuset for a server in auto mode.
// Returns "" when no topology is available (caller falls back to the node default).
func (s *CPUPinningService) AutoCpuset(ctx context.Context, nodeToken string, nodeID, excludeServerID int, cpuLimit float64) (string, error) {
	topo, err := s.GetNodeTopology(ctx, nodeToken)
	if err != nil {
		return "", err
	}
	if topo == nil {
		return "", nil
	}
	load, err := s.NodeCoreLoad(nodeID, excludeServerID)
	if err != nil {
		return "", err
	}
	budget := int(math.Ceil(cpuLimit))
	if budget < 1 {
		budget = 1
	}
	return computeAutoCpuset(topo, budget, cpuReserveHostCores, load), nil
}

// ValidateCpuset checks a manual cpuset against the node topology: every listed
// core must exist. If the topology is unavailable it cannot validate, so it
// accepts the value (best-effort) rather than blocking the operator.
func (s *CPUPinningService) ValidateCpuset(ctx context.Context, nodeToken, cpuset string) error {
	ids := parseCpusetList(cpuset)
	if len(ids) == 0 {
		return fmt.Errorf("cpuset is empty")
	}
	topo, err := s.GetNodeTopology(ctx, nodeToken)
	if err != nil || topo == nil {
		return nil // cannot validate; accept
	}
	valid := map[int]bool{}
	for _, c := range topo.Cores {
		valid[c.ID] = true
	}
	for _, id := range ids {
		if !valid[id] {
			return fmt.Errorf("core %d does not exist on this node (logical cores 0-%d)", id, topo.LogicalCount-1)
		}
	}
	return nil
}

// computeAutoCpuset picks `budget` cores, preferring performance cores, then
// least-loaded, then lowest id, while reserving reserveHostCores for the host.
// Pure + deterministic.
func computeAutoCpuset(topo *CPUTopology, budget, reserveHostCores int, load map[int]int) string {
	if topo == nil || len(topo.Cores) == 0 || budget <= 0 {
		return ""
	}
	maxAssignable := topo.LogicalCount - reserveHostCores
	if maxAssignable < 1 {
		maxAssignable = 1
	}
	if budget > maxAssignable {
		budget = maxAssignable
	}

	cores := make([]CPUCore, len(topo.Cores))
	copy(cores, topo.Cores)
	sort.SliceStable(cores, func(i, j int) bool {
		ri, rj := coreTypeRank(cores[i].Type), coreTypeRank(cores[j].Type)
		if ri != rj {
			return ri < rj
		}
		li, lj := load[cores[i].ID], load[cores[j].ID]
		if li != lj {
			return li < lj
		}
		return cores[i].ID < cores[j].ID
	})

	chosen := make([]int, 0, budget)
	for i := 0; i < budget && i < len(cores); i++ {
		chosen = append(chosen, cores[i].ID)
	}
	sort.Ints(chosen)
	return compactCpuset(chosen)
}

func coreTypeRank(t string) int {
	switch t {
	case "P":
		return 0
	case "E":
		return 2
	default: // "standard" / unknown
		return 1
	}
}

// compactCpuset renders sorted ids as a cpuset string, collapsing runs:
// [0 1 2 3 8] -> "0-3,8".
func compactCpuset(ids []int) string {
	if len(ids) == 0 {
		return ""
	}
	var parts []string
	start, prev := ids[0], ids[0]
	flush := func() {
		if start == prev {
			parts = append(parts, strconv.Itoa(start))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", start, prev))
		}
	}
	for _, id := range ids[1:] {
		if id == prev+1 {
			prev = id
			continue
		}
		flush()
		start, prev = id, id
	}
	flush()
	return strings.Join(parts, ",")
}

// parseCpusetList expands "0-3,8" into [0 1 2 3 8]. Invalid tokens are skipped.
func parseCpusetList(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i := strings.IndexByte(part, '-'); i >= 0 {
			lo, err1 := strconv.Atoi(strings.TrimSpace(part[:i]))
			hi, err2 := strconv.Atoi(strings.TrimSpace(part[i+1:]))
			if err1 != nil || err2 != nil || hi < lo {
				continue
			}
			for n := lo; n <= hi; n++ {
				out = append(out, n)
			}
		} else if n, err := strconv.Atoi(part); err == nil {
			out = append(out, n)
		}
	}
	return out
}
