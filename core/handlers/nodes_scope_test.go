package handlers

import (
	"testing"

	"dylaris-core/models"
)

// The bug these exist to fix: GetNodes returned the whole fleet to an admin, so
// swarm hosts showed up under "my machines" next to the customer's own hardware.
// A swarm host must land in NEITHER of the two kinds the page can name.
func TestNodeScopePredicates(t *testing.T) {
	owner := "11111111-1111-1111-1111-111111111111"

	tests := []struct {
		name     string
		node     models.Node
		external bool
		byon     bool
	}{
		{
			name:     "a swarm host is neither",
			node:     models.Node{Name: "swarm-1"},
			external: false,
			byon:     false,
		},
		{
			name:     "a tagged swarm host is still not external if it has no tag",
			node:     models.Node{Name: "swarm-2", Tags: "eu,ssd"},
			external: false,
			byon:     false,
		},
		{
			name:     "the operator's own external machine",
			node:     models.Node{Name: "office-box", Tags: "external"},
			external: true,
			byon:     false,
		},
		{
			name:     "external among other tags",
			node:     models.Node{Name: "office-box", Tags: "eu,external,ssd"},
			external: true,
			byon:     false,
		},
		{
			// The one that must not cross over: a customer machine is external
			// too, but it belongs to the customer's tab, not the operator's.
			name:     "a customer machine is BYON, never external",
			node:     models.Node{Name: "home-desktop", Tags: "external", OwnerID: &owner},
			external: false,
			byon:     true,
		},
		{
			// A BYON node that has not reconnected since nodes started reporting
			// the tag. Still the customer's, still on the BYON tab.
			name:     "an untagged owned node is still BYON",
			node:     models.Node{Name: "home-desktop", OwnerID: &owner},
			external: false,
			byon:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isExternalPlatformNode(tt.node); got != tt.external {
				t.Errorf("isExternalPlatformNode = %v, want %v", got, tt.external)
			}
			if got := isBYONNode(tt.node); got != tt.byon {
				t.Errorf("isBYONNode = %v, want %v", got, tt.byon)
			}
			if isExternalPlatformNode(tt.node) && isBYONNode(tt.node) {
				t.Error("a node landed in both tabs; the two kinds must be disjoint")
			}
		})
	}
}

func TestFilterNodesKeepsOrderAndNeverReturnsNil(t *testing.T) {
	owner := "22222222-2222-2222-2222-222222222222"
	in := []models.Node{
		{Name: "a", OwnerID: &owner},
		{Name: "b"},
		{Name: "c", OwnerID: &owner},
	}
	got := filterNodes(in, isBYONNode)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("filterNodes = %+v, want a then c", got)
	}
	// The panel reads res.nodes as an array; a null would make it fall over.
	if empty := filterNodes(in, isExternalPlatformNode); empty == nil {
		t.Error("filterNodes returned nil, want an empty slice so the JSON stays []")
	}
}
