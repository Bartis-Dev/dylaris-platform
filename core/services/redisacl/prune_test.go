package redisacl

import (
	"sort"
	"strings"
	"testing"
)

func TestExpectedNodeACLUsersCoversAllThree(t *testing.T) {
	got := ExpectedNodeACLUsers([]string{"tok-a"})
	for _, want := range []string{"node-tok-a", "node-tok-a-shipper", "node-tok-a-link"} {
		if !got[want] {
			t.Errorf("expected set is missing %q", want)
		}
	}
	if len(got) != 3 {
		t.Errorf("expected set has %d entries for one token, want 3: %v", len(got), got)
	}
}

// TestUnknownNodeACLUsers is the safety surface of the prune: everything it
// returns gets DELUSER'd, so a false positive deletes a live node's credential.
func TestUnknownNodeACLUsers(t *testing.T) {
	live := "aaaaaaaa-1111-2222-3333-444444444444"
	dead := "bbbbbbbb-5555-6666-7777-888888888888"
	expected := ExpectedNodeACLUsers([]string{live})

	users := []string{
		"default",
		// Route-only link kits: generateLinkIdentity returns "link-"+hex, so a
		// link kit username can never collide with the node- namespace. Pinned
		// here because the whole prune rests on that.
		"link-0123456789abcdef0123456789abcdef",
		// Other subsystems on the same Redis.
		"gw-warp",
		"hub-admin",
		NodeUsername(live), ShipperUsername(live), LinkUsername(live),
		NodeUsername(dead), ShipperUsername(dead), LinkUsername(dead),
	}

	got := UnknownNodeACLUsers(users, expected)
	sort.Strings(got)
	want := []string{NodeUsername(dead), LinkUsername(dead), ShipperUsername(dead)}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("got %d orphans %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("orphan[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Stated separately from the count so a regression says WHICH invariant broke.
	for _, u := range got {
		if strings.Contains(u, live) {
			t.Errorf("prune would delete %q, which belongs to a live node", u)
		}
		if !strings.HasPrefix(u, nodeACLPrefix) {
			t.Errorf("prune would delete %q, outside Core's node- namespace", u)
		}
	}
}

// TestUnknownNodeACLUsersEmptyExpected pins the shape the reconciler's own guard
// exists for: with nothing expected, EVERY node- user is an orphan. That is
// correct here and is exactly why the caller refuses to run this on an empty
// node list.
func TestUnknownNodeACLUsersEmptyExpected(t *testing.T) {
	users := []string{"default", "link-abc", "node-x", "node-x-link"}
	got := UnknownNodeACLUsers(users, map[string]bool{})
	if len(got) != 2 {
		t.Fatalf("got %v, want both node- users", got)
	}
}
