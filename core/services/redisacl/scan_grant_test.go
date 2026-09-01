package redisacl

import (
	"strings"
	"testing"
)

// SCAN is a keyspace-wide read of NAMES, and Redis does not filter it by the
// ACL's key patterns.
//
// Measured against Valkey 8 rather than assumed: a user scoped to a single
// prefix ran SCAN and got back keys belonging to every other prefix; a GET on
// one of them then answered NOPERM. Values are protected, names are not - and
// the names are the sensitive part here, because every server UUID, node token,
// SFTP account name and link token on the platform is a key name, and those are
// exactly the identifiers a forged write needs as input.
//
// Only the node agent may hold it, and only because it genuinely walks the
// keyspace (Core discovery, port allocation, the disk-full sweep). Everything
// else here runs somewhere a tenant can reach: the log-shipper credential is in
// the environment of the tenant's own Minecraft container, beside plugins the
// tenant wrote, and it never called SCAN once.
func TestOnlyTheNodeAgentMayScan(t *testing.T) {
	cases := []struct {
		name  string
		rules []interface{}
		want  bool
	}{
		{"node agent", BuildNodeACLRules("node-a", "pw", []string{"srv-1"}), true},
		{"log-shipper in the tenant's container", BuildShipperACLRules("pw", "srv-1"), false},
		{"the node's link sidecar", BuildLinkACLRules("pw", "node-a", "tok"), false},
		{"a route-only link on a customer machine", BuildRouteOnlyLinkACLRules("pw", "tok", "link-abc"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := grants(c.rules, "+scan"); got != c.want {
				if c.want {
					t.Error("the node agent lost +scan; its keyspace walks would fail with NOPERM")
				} else {
					t.Error("this principal may enumerate every key name on the platform")
				}
			}
			// KEYS must stay denied for all four, which -@dangerous does.
			if !grants(c.rules, "-@dangerous") {
				t.Error("KEYS is no longer denied")
			}
		})
	}
}

// The route-only link's error stream is named by its link id, never by a slice
// of its token.
//
// The instance id becomes a Redis KEY NAME, and key names are readable by
// anything that can SCAN. It used to be tunnelToken[:8] on both sides, so eight
// hex characters of a live authentication token were published to every other
// tenant's machine, in a key with no expiry.
func TestRouteOnlyLinkErrorStreamCarriesNoSecret(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	const linkID = "link-94784d508b87a684501786a98b482484"

	for _, r := range BuildRouteOnlyLinkACLRules("pw", token, linkID) {
		s, ok := r.(string)
		if !ok || !strings.Contains(s, "dylaris:errors:link:") {
			continue
		}
		if strings.Contains(s, token[:8]) {
			t.Errorf("the error-stream grant %q carries a prefix of the tunnel token", s)
		}
		if !strings.Contains(s, linkID) {
			t.Errorf("the error-stream grant %q is not named by the link id, so the link's own writes get NOPERM", s)
		}
	}
}

func grants(rules []interface{}, want string) bool {
	for _, r := range rules {
		if s, ok := r.(string); ok && s == want {
			return true
		}
	}
	return false
}
