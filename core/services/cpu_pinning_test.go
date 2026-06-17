package services

import (
	"reflect"
	"testing"
)

func TestParseCpusetList(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"", nil},
		{"5", []int{5}},
		{"0-3,8", []int{0, 1, 2, 3, 8}},
		{" 0 - 2 , 6 ", []int{0, 1, 2, 6}},
		{"4-2", nil},
	}
	for _, c := range cases {
		if got := parseCpusetList(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseCpusetList(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCompactCpuset(t *testing.T) {
	cases := []struct {
		in   []int
		want string
	}{
		{nil, ""},
		{[]int{5}, "5"},
		{[]int{0, 1, 2, 3}, "0-3"},
		{[]int{0, 1, 2, 3, 8}, "0-3,8"},
		{[]int{0, 2, 4}, "0,2,4"},
	}
	for _, c := range cases {
		if got := compactCpuset(c.in); got != c.want {
			t.Errorf("compactCpuset(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func hybrid8() *CPUTopology {
	// cores 0-3 = P, 4-7 = E
	cores := make([]CPUCore, 8)
	for i := 0; i < 8; i++ {
		t := "P"
		if i >= 4 {
			t = "E"
		}
		cores[i] = CPUCore{ID: i, Type: t, Sibling: -1}
	}
	return &CPUTopology{LogicalCount: 8, PhysicalCount: 8, Hybrid: true, Cores: cores}
}

func TestComputeAutoCpuset_PrefersPCores(t *testing.T) {
	got := computeAutoCpuset(hybrid8(), 2, 1, nil, nil)
	if got != "0-1" {
		t.Fatalf("budget 2 should pick the first two P cores, got %q", got)
	}
}

func TestComputeAutoCpuset_ReservesHostCore(t *testing.T) {
	// budget 8 but reserve 1 -> at most 7 cores assignable.
	got := computeAutoCpuset(hybrid8(), 8, 1, nil, nil)
	if got != "0-6" {
		t.Fatalf("expected 7 cores 0-6 (1 reserved), got %q", got)
	}
}

func TestComputeAutoCpuset_SpreadByLoad(t *testing.T) {
	// All P cores; core 0 already heavily loaded, so least-loaded P cores win.
	load := map[int]int{0: 5, 1: 0, 2: 0, 3: 0}
	topo := &CPUTopology{LogicalCount: 4, PhysicalCount: 4, Cores: []CPUCore{
		{ID: 0, Type: "P"}, {ID: 1, Type: "P"}, {ID: 2, Type: "P"}, {ID: 3, Type: "P"},
	}}
	got := computeAutoCpuset(topo, 2, 1, load, nil)
	if got != "1-2" {
		t.Fatalf("expected least-loaded cores 1-2, got %q", got)
	}
}

func TestComputeAutoCpuset_RestrictedToNodePool(t *testing.T) {
	// 8 cores but the node pool is only 4-7 (E cores); auto must stay inside it.
	// No host reserve when a pool is set, so budget 2 -> first two pool cores.
	allowed := map[int]bool{4: true, 5: true, 6: true, 7: true}
	got := computeAutoCpuset(hybrid8(), 2, 0, nil, allowed)
	if got != "4-5" {
		t.Fatalf("expected cores from the node pool 4-5, got %q", got)
	}
}

func TestComputeAutoCpuset_Empty(t *testing.T) {
	if got := computeAutoCpuset(nil, 2, 1, nil, nil); got != "" {
		t.Fatalf("nil topology should yield empty, got %q", got)
	}
	if got := computeAutoCpuset(&CPUTopology{}, 0, 1, nil, nil); got != "" {
		t.Fatalf("zero budget should yield empty, got %q", got)
	}
}

func TestTopologySignature_StableExceptHardware(t *testing.T) {
	a := &CPUTopology{LogicalCount: 8, PhysicalCount: 8, Hybrid: true,
		Cores: []CPUCore{{ID: 0, Type: "P"}, {ID: 1, Type: "E"}}, ScannedAt: 100}
	b := &CPUTopology{LogicalCount: 8, PhysicalCount: 8, Hybrid: true,
		Cores: []CPUCore{{ID: 0, Type: "P"}, {ID: 1, Type: "E"}}, ScannedAt: 999} // different scan time
	if TopologySignature(a) != TopologySignature(b) {
		t.Fatal("signature must ignore scannedAt")
	}
	c := &CPUTopology{LogicalCount: 4, PhysicalCount: 4, Cores: []CPUCore{{ID: 0, Type: "standard"}}}
	if TopologySignature(a) == TopologySignature(c) {
		t.Fatal("different hardware must yield a different signature")
	}
}
