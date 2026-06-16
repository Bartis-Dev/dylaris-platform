package services

import (
	"strings"

	"dylaris-core/models"
	"dylaris-core/store"
)

// This file holds the node load/capacity math shared by the placement handler
// (handlers.scoreNode) and the rebalance worker. Keeping it in one place means
// the scheduler and the rebalancer agree on what "full" and "loaded" mean.

// NodeRAMLoad and NodeCPULoad are the per-resource utilization fractions used
// by the scheduler. Each is allocated / (physical * overcommit), 0 when the
// physical total is unknown (heartbeat not yet seen).

// NodeRAMLoad returns the RAM utilization fraction (0..n) for a node.
func NodeRAMLoad(allocRAMMB, totalRAMMB int64, ramOvercommit float64) float64 {
	if totalRAMMB <= 0 {
		return 0
	}
	return float64(allocRAMMB) / (float64(totalRAMMB) * ramOvercommit)
}

// NodeCPULoad returns the CPU utilization fraction (0..n) for a node.
func NodeCPULoad(allocCPU, totalCPU, cpuOvercommit float64) float64 {
	if totalCPU <= 0 {
		return 0
	}
	return allocCPU / (totalCPU * cpuOvercommit)
}

// nodeLoadPercent reduces a node's RAM and CPU utilization to a single load
// percent, taking the binding (max) of the two. RAM is usually the constraint
// for a Minecraft fleet, but a node can also be CPU-bound under a low CPU
// overcommit ratio, so we take whichever resource is tighter.
func nodeLoadPercent(allocRAMMB int64, allocCPU float64, totalRAMMB int64, totalCPU, ramOvercommit, cpuOvercommit float64) float64 {
	ram := NodeRAMLoad(allocRAMMB, totalRAMMB, ramOvercommit)
	cpu := NodeCPULoad(allocCPU, totalCPU, cpuOvercommit)
	load := ram
	if cpu > load {
		load = cpu
	}
	return load * 100
}

// nodeHasCapacityFor reports whether the target node has room for the server's
// RAM and CPU request under its overcommit ratios, and enough free disk. Mirrors
// the Available checks in handlers.scoreNode so a rebalance move never lands on
// a node the scheduler would have rejected.
func nodeHasCapacityFor(s store.Store, n *models.Node, hb *NodeHeartbeat, srv *models.Server) bool {
	allocRAM, allocCPU, err := s.SumAllocatedByNode(n.ID)
	if err != nil {
		return false
	}

	// RAM check.
	if n.TotalRAMMB > 0 {
		cap := float64(n.TotalRAMMB) * n.RAMOvercommitRatio
		if float64(allocRAM+int64(srv.Memory)) > cap {
			return false
		}
	}
	// CPU check (only when the server requests a positive limit).
	if srv.CPULimit > 0 && n.TotalCPU > 0 {
		cap := n.TotalCPU * n.CPUOvercommitRatio
		if allocCPU+srv.CPULimit > cap {
			return false
		}
	}
	// Disk floor: pick the largest single path (a server lives on one disk) and
	// keep a 5 GB buffer. Skip when the disk request or heartbeat is unknown.
	if srv.DiskLimit > 0 && hb != nil {
		maxFreeGB := int64(0)
		for _, sp := range hb.Storage {
			gb := sp.FreeBytes / (1024 * 1024 * 1024)
			if gb > maxFreeGB {
				maxFreeGB = gb
			}
		}
		if maxFreeGB > 0 && maxFreeGB < srv.DiskLimit+5 {
			return false
		}
	}
	return true
}

// equalRegion compares two region keys case-insensitively (region values are
// stored lowercased by convention but be defensive).
func equalRegion(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// nodeHasAllTagsCSV reports whether every tag in the comma-separated required
// list is present on the comma-separated have list (AND semantics, matching
// handlers.nodeHasAllTags). An empty required list is satisfied by any node.
func nodeHasAllTagsCSV(haveCSV, requiredCSV string) bool {
	have := map[string]bool{}
	for _, t := range strings.Split(haveCSV, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			have[t] = true
		}
	}
	for _, t := range strings.Split(requiredCSV, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if !have[t] {
			return false
		}
	}
	return true
}
