package services

import (
	"reflect"
	"testing"

	"dylaris-pkg/storageplacement"
)

// resolvePlacement decides which disk a server lands on. Core has to reach the
// same answer the node will, so the rule is pinned here rather than left to a
// comment.
func TestResolvePlacement(t *testing.T) {
	fleet := NodePlacement{Mode: storageplacement.ModeManual, Order: []string{"/fleet"}}

	tests := []struct {
		name string
		own  NodePlacement
		want NodePlacement
	}{
		{
			name: "a node with its own order keeps it",
			own:  NodePlacement{Mode: storageplacement.ModeAuto, Order: []string{"/own"}},
			want: NodePlacement{Mode: storageplacement.ModeAuto, Order: []string{"/own"}},
		},
		{
			// Manual with no order is still a decision - the operator turned the
			// mode off - so it must not silently inherit the fleet's order.
			name: "manual mode alone counts as a decision",
			own:  NodePlacement{Mode: storageplacement.ModeManual},
			want: NodePlacement{Mode: storageplacement.ModeManual},
		},
		{
			// Plain auto with no order carries no information, so it falls
			// through rather than pinning the node to auto forever.
			name: "an empty node policy falls through to the fleet",
			own:  NodePlacement{Mode: storageplacement.ModeAuto},
			want: fleet,
		},
		{
			name: "a zero-value node policy falls through too",
			own:  NodePlacement{},
			want: fleet,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvePlacement(tt.own, fleet)
			if got.Mode != tt.want.Mode || !reflect.DeepEqual(got.Order, tt.want.Order) {
				t.Errorf("resolvePlacement = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// With no fleet default configured, an empty node policy stays empty rather
// than inventing one.
func TestResolvePlacement_NoFleetDefault(t *testing.T) {
	got := resolvePlacement(NodePlacement{}, NodePlacement{Mode: storageplacement.ModeAuto})
	if got.Mode != storageplacement.ModeAuto || len(got.Order) != 0 {
		t.Errorf("resolvePlacement = %+v, want empty auto", got)
	}
}

func TestDecodeFleetPlacement(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantMode  string
		wantOrder []string
	}{
		{"a stored policy", `{"mode":"manual","order":["/a","/b"]}`, storageplacement.ModeManual, []string{"/a", "/b"}},
		{"empty value is auto", "", storageplacement.ModeAuto, nil},
		// An unreadable default must not stop a server from being placed; it
		// just stops expressing a preference.
		{"malformed json is auto", `{not json`, storageplacement.ModeAuto, nil},
		// An unknown mode normalises rather than being rejected, matching what
		// the node does with the same value.
		{"an unknown mode normalises to auto", `{"mode":"sideways","order":["/a"]}`, storageplacement.ModeAuto, []string{"/a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeFleetPlacement(tt.raw)
			if got.Mode != tt.wantMode {
				t.Errorf("Mode = %q, want %q", got.Mode, tt.wantMode)
			}
			if !reflect.DeepEqual(got.Order, tt.wantOrder) {
				t.Errorf("Order = %v, want %v", got.Order, tt.wantOrder)
			}
		})
	}
}

func TestCollectStoragePaths(t *testing.T) {
	heartbeats := map[string]*NodeHeartbeat{
		"node-a": {Storage: []HeartbeatStoragePath{{Path: "/storage"}, {Path: "/fast"}}},
		"node-b": {Storage: []HeartbeatStoragePath{{Path: "/storage"}, {Path: "/bulk"}}},
	}
	got := collectStoragePaths(heartbeats)

	// Sorted, not first-seen: heartbeats arrive as a map and Go randomises map
	// iteration, so a first-seen order would reshuffle the editor's list on
	// every page load.
	want := []string{"/bulk", "/fast", "/storage"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collectStoragePaths = %v, want %v", got, want)
	}
}

// Running it repeatedly must give the same answer, which is the whole reason
// for sorting.
func TestCollectStoragePaths_IsStable(t *testing.T) {
	heartbeats := map[string]*NodeHeartbeat{
		"a": {Storage: []HeartbeatStoragePath{{Path: "/z"}}},
		"b": {Storage: []HeartbeatStoragePath{{Path: "/y"}}},
		"c": {Storage: []HeartbeatStoragePath{{Path: "/x"}}},
		"d": {Storage: []HeartbeatStoragePath{{Path: "/w"}}},
	}
	first := collectStoragePaths(heartbeats)
	for i := 0; i < 20; i++ {
		if got := collectStoragePaths(heartbeats); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d gave %v, first run gave %v", i, got, first)
		}
	}
}

func TestCollectStoragePaths_SkipsEmptyAndNil(t *testing.T) {
	heartbeats := map[string]*NodeHeartbeat{
		"node-a": {Storage: []HeartbeatStoragePath{{Path: ""}, {Path: "/real"}}},
		"node-b": nil,
		"node-c": {},
	}
	got := collectStoragePaths(heartbeats)
	if !reflect.DeepEqual(got, []string{"/real"}) {
		t.Errorf("collectStoragePaths = %v, want just the real path", got)
	}
}

// Never nil: the editor maps over this directly.
func TestCollectStoragePaths_EmptyFleet(t *testing.T) {
	got := collectStoragePaths(nil)
	if got == nil {
		t.Fatal("collectStoragePaths(nil) = nil, want an empty slice")
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
