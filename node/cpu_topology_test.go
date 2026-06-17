package main

import (
	"reflect"
	"strconv"
	"testing"
)

func TestParseCPUList(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"", nil},
		{"0", []int{0}},
		{"0-3", []int{0, 1, 2, 3}},
		{"0-3,8,10-11", []int{0, 1, 2, 3, 8, 10, 11}},
		{" 0 - 2 , 5 ", []int{0, 1, 2, 5}},
		{"3-1", nil},      // inverted range ignored
		{"x,2", []int{2}}, // junk token ignored
	}
	for _, c := range cases {
		got := parseCPUList(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseCPUList(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCompactCPUList(t *testing.T) {
	cases := []struct {
		in   []int
		want string
	}{
		{nil, ""},
		{[]int{2}, "2"},
		{[]int{0, 1, 2, 3}, "0-3"},
		{[]int{3, 1, 0, 2, 8}, "0-3,8"}, // unsorted input
	}
	for _, c := range cases {
		if got := compactCPUList(c.in); got != c.want {
			t.Errorf("compactCPUList(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeCpusetForHost(t *testing.T) {
	// getCPUTopology() falls back to a uniform NumCPU layout on the test host,
	// so cores 0..N-1 exist. A core well beyond that must be dropped.
	n := getCPUTopology().LogicalCount
	if n < 1 {
		t.Skip("no cores reported")
	}
	// In-range core is kept.
	if got := sanitizeCpusetForHost("0", "test"); got != "0" {
		t.Fatalf("in-range core dropped: %q", got)
	}
	// Far out-of-range core is dropped -> empty (unpinned).
	if got := sanitizeCpusetForHost(strconv.Itoa(n+100), "test"); got != "" {
		t.Fatalf("out-of-range core not dropped: %q", got)
	}
	// Empty stays empty.
	if got := sanitizeCpusetForHost("", "test"); got != "" {
		t.Fatalf("empty should stay empty: %q", got)
	}
}

func TestUniformTopology(t *testing.T) {
	topo := uniformTopology()
	if topo.LogicalCount != len(topo.Cores) {
		t.Fatalf("LogicalCount %d != len(Cores) %d", topo.LogicalCount, len(topo.Cores))
	}
	if topo.LogicalCount == 0 {
		t.Fatal("expected at least one core")
	}
	for i, c := range topo.Cores {
		if c.ID != i || c.Type != "standard" || c.Sibling != -1 {
			t.Fatalf("core %d unexpected: %+v", i, c)
		}
	}
	if topo.Hybrid {
		t.Fatal("uniform topology must not be hybrid")
	}
}
