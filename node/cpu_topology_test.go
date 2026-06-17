package main

import (
	"reflect"
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
