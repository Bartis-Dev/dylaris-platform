package redisacl

import (
	"strings"
	"testing"
)

// TestOmittedUUIDIsARevocationNotASkip pins the property that makes an
// incomplete server list dangerous rather than merely incomplete.
//
// BuildNodeACLRules opens with "resetkeys", so ACL SETUSER does not ADD the
// patterns that follow - it REPLACES the user's whole key scope with them. A
// uuid missing from serverUUIDs is therefore not "left alone until next time":
// its grant is dropped from a live node on the spot, and the node loses
// console, status, stats and commands for a server that is still running.
//
// The producer of that slice is store.ListServersByNode, which used to swallow
// a failed row scan and a cut result set and return a short list with a nil
// error. This test is the other half of that coupling: it is why the store
// must report a short read instead of returning it.
func TestOmittedUUIDIsARevocationNotASkip(t *testing.T) {
	const token = "node-token-1"
	full := []string{"uuid-a", "uuid-b"}
	short := []string{"uuid-a"} // uuid-b lost to a failed scan

	for _, tc := range []struct {
		name  string
		build func([]string) []interface{}
	}{
		{"node", func(u []string) []interface{} { return BuildNodeACLRules(token, "pw", u) }},
		{"shipper", func(u []string) []interface{} { return BuildShipperACLRules("pw", u) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules := tc.build(short)

			// The reset is what turns an omission into a revocation. If this
			// ever stops being true the severity of a short read changes, so
			// assert it here rather than assuming it.
			if !hasRule(rules, "resetkeys") {
				t.Fatal("BuildACLRules no longer starts from resetkeys; re-check whether an omitted uuid still revokes")
			}
			if hasRule(rules, "~dylaris:server:uuid-b:*") {
				t.Error("uuid-b was omitted from the list but still granted; the test no longer proves anything")
			}
			if !hasRule(rules, "~dylaris:server:uuid-a:*") {
				t.Error("uuid-a was in the list but not granted")
			}

			// The same node built from the complete list keeps both, which is
			// what the platform silently lost.
			if !hasRule(tc.build(full), "~dylaris:server:uuid-b:*") {
				t.Error("uuid-b is not granted even from the complete list")
			}
		})
	}
}

func hasRule(rules []interface{}, want string) bool {
	for _, r := range rules {
		if s, ok := r.(string); ok && strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}
