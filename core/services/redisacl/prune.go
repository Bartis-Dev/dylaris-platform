package redisacl

import "strings"

// nodeACLPrefix is the namespace Core owns for per-node ACL users:
// NodeUsername/ShipperUsername/LinkUsername all start with it. Route-only link
// kits deliberately do NOT (RouteOnlyLinkUsername is the bare link id), and
// neither does anything else on this Redis - the hub admin, the warp leader,
// `default`. Restricting the prune to this prefix is what keeps it from
// touching a user Core did not create.
const nodeACLPrefix = "node-"

// UnknownNodeACLUsers returns the members of `users` that live in Core's
// per-node ACL namespace but are not in `expected`.
//
// This exists because nothing ever removed them. The reconciler only ENSURES
// ACLs for nodes it can see, and node ACL teardown at delete time is
// best-effort, so a failed DELUSER - or a node row that vanished by any route
// other than the panel's delete - left three live scoped credentials behind for
// good. A live stack was observed with three node identities in ACL LIST while
// the database knew one.
//
// Matching is on the whole username, never on a parsed-out token: the three
// names for one node differ only by suffix, and comparing whole strings against
// the set the caller says should exist cannot mis-parse its way into deleting a
// live node's credential.
func UnknownNodeACLUsers(users []string, expected map[string]bool) []string {
	var out []string
	for _, u := range users {
		if !strings.HasPrefix(u, nodeACLPrefix) {
			continue
		}
		if expected[u] {
			continue
		}
		out = append(out, u)
	}
	return out
}

// ExpectedNodeACLUsers is the full set of ACL usernames that should exist for
// the given node tokens: the agent, its shipper and its link user.
func ExpectedNodeACLUsers(tokens []string) map[string]bool {
	m := make(map[string]bool, len(tokens)*3)
	for _, t := range tokens {
		m[NodeUsername(t)] = true
		m[ShipperUsername(t)] = true
		m[LinkUsername(t)] = true
	}
	return m
}
