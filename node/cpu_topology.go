package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CPUCore describes one logical CPU as the panel needs it for the pinning grid.
// Type is "P" (performance), "E" (efficiency) or "standard" (uniform / unknown).
// Sibling is the hyperthread partner's logical id, or -1 when none / unknown.
type CPUCore struct {
	ID      int    `json:"id"`
	Type    string `json:"type"`
	Sibling int    `json:"sibling"`
}

// CPUTopology is the host CPU layout reported to Core for the pinning UI.
type CPUTopology struct {
	LogicalCount  int       `json:"logicalCount"`
	PhysicalCount int       `json:"physicalCount"`
	Hybrid        bool      `json:"hybrid"`
	Cores         []CPUCore `json:"cores"`
	ScannedAt     int64     `json:"scannedAt"`
}

var (
	cpuTopologyOnce sync.Once
	cpuTopology     *CPUTopology
)

// getCPUTopology scans the host once and caches it. A hardware change needs a
// node restart anyway (which re-runs this), so a one-shot scan is sufficient and
// avoids re-reading sysfs every heartbeat.
func getCPUTopology() *CPUTopology {
	cpuTopologyOnce.Do(func() {
		cpuTopology = scanCPUTopology()
	})
	return cpuTopology
}

// scanCPUTopology reads Linux sysfs. On non-Linux hosts (dev) or when sysfs is
// unavailable it falls back to a uniform NumCPU layout.
func scanCPUTopology() *CPUTopology {
	const base = "/sys/devices/system/cpu"
	entries, err := os.ReadDir(base)
	if err != nil {
		return uniformTopology()
	}

	var ids []int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "cpu") {
			continue
		}
		n, err := strconv.Atoi(name[len("cpu"):]) // skip cpufreq, cpuidle, etc.
		if err != nil {
			continue
		}
		ids = append(ids, n)
	}
	if len(ids) == 0 {
		return uniformTopology()
	}
	sort.Ints(ids)

	// Intel hybrid (Linux >= 5.16) exposes the P/E split via per-PMU cpu lists.
	pSet := readCPUSet(base + "/../cpu_core/cpus")
	if len(pSet) == 0 {
		pSet = readCPUSet("/sys/devices/cpu_core/cpus")
	}
	eSet := readCPUSet("/sys/devices/cpu_atom/cpus")
	hybrid := len(pSet) > 0 && len(eSet) > 0

	// ARM big.LITTLE (and some others) expose per-core cpu_capacity; a spread of
	// capacities means heterogeneous cores even without the hybrid PMUs.
	caps := map[int]int{}
	maxCap := 0
	if !hybrid {
		for _, id := range ids {
			if c, ok := readIntFile(fmt.Sprintf("%s/cpu%d/cpu_capacity", base, id)); ok {
				caps[id] = c
				if c > maxCap {
					maxCap = c
				}
			}
		}
	}
	capHetero := false
	for _, c := range caps {
		if c != maxCap {
			capHetero = true
			break
		}
	}

	physical := map[string]bool{}
	cores := make([]CPUCore, 0, len(ids))
	for _, id := range ids {
		ctype := "standard"
		switch {
		case hybrid && pSet[id]:
			ctype = "P"
		case hybrid && eSet[id]:
			ctype = "E"
		case capHetero && len(caps) > 0:
			if caps[id] == maxCap {
				ctype = "P"
			} else {
				ctype = "E"
			}
		}

		sibling := -1
		for _, s := range readCPUListSorted(fmt.Sprintf("%s/cpu%d/topology/thread_siblings_list", base, id)) {
			if s != id {
				sibling = s
				break
			}
		}

		coreID, _ := readIntFile(fmt.Sprintf("%s/cpu%d/topology/core_id", base, id))
		pkgID, _ := readIntFile(fmt.Sprintf("%s/cpu%d/topology/physical_package_id", base, id))
		physical[fmt.Sprintf("%d:%d", pkgID, coreID)] = true

		cores = append(cores, CPUCore{ID: id, Type: ctype, Sibling: sibling})
	}

	return &CPUTopology{
		LogicalCount:  len(ids),
		PhysicalCount: len(physical),
		Hybrid:        hybrid || capHetero,
		Cores:         cores,
		ScannedAt:     time.Now().Unix(),
	}
}

func uniformTopology() *CPUTopology {
	n := runtime.NumCPU()
	cores := make([]CPUCore, n)
	for i := 0; i < n; i++ {
		cores[i] = CPUCore{ID: i, Type: "standard", Sibling: -1}
	}
	return &CPUTopology{
		LogicalCount:  n,
		PhysicalCount: n,
		Hybrid:        false,
		Cores:         cores,
		ScannedAt:     time.Now().Unix(),
	}
}

// readCPUSet parses a sysfs cpu list ("0-3,8,10-11") into a presence set.
func readCPUSet(path string) map[int]bool {
	set := map[int]bool{}
	for _, id := range parseCPUList(readFileTrim(path)) {
		set[id] = true
	}
	return set
}

func readCPUListSorted(path string) []int {
	list := parseCPUList(readFileTrim(path))
	sort.Ints(list)
	return list
}

// parseCPUList expands "0-3,8,10-11" into [0 1 2 3 8 10 11].
func parseCPUList(s string) []int {
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

func readFileTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readIntFile(path string) (int, bool) {
	s := readFileTrim(path)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}
