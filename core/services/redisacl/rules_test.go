package redisacl

import (
	"strings"
	"testing"
)

func joinRules(r []interface{}) string {
	parts := make([]string, len(r))
	for i, v := range r {
		parts[i] = v.(string)
	}
	return strings.Join(parts, " ")
}

func TestBuildNodeACLRules(t *testing.T) {
	r := joinRules(BuildNodeACLRules("n1", "pw", []string{"uuid-a"}))
	for _, want := range []string{
		"on", ">pw", "resetkeys", "resetchannels",
		"~dylaris:node:n1:*", "~dylaris:discovery:n1",
		// The un-prefixed node:<token>:* namespace holds the per-server
		// storage-path mapping (node:n1:server:<uuid>:storage); without it the
		// node gets NOPERM on every install + reconcile.
		"~node:n1:*",
		"~dylaris:server:uuid-a:*", "~dylaris:core:*", "~sftp:auth:*",
		// Upload-limit enforcement needs the node to read the config keys and
		// read+write the shared per-user daily counter; SFTP needs its own
		// server-list key. Without these the node-side quota fails open and SFTP
		// sees an empty root under mandatory ACL.
		"beam:max_upload_bytes", "beam:daily_upload_bytes", "~dylaris:beam:daily:*",
		"sftp:node:n1:",
		"&dylaris:backup:results", "&dylaris:server:uuid-a:stats:live",
		"+@read", "+@write", "+@stream", "+@pubsub", "-@dangerous", "+scan",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("node rules missing %q in: %s", want, r)
		}
	}
}

// TestNodeGlobalKeysAreReadOnlyWhereTheNodeOnlyReads pins least privilege on the
// GLOBAL (not node-scoped) keys. A read+write grant here is a write handed to
// every node in the fleet, tenant-owned BYON nodes included, and three of these
// are the bandwidth throttle the node itself is subject to.
//
// Token comparison, not strings.Contains: "~beam:bw_limit" is a substring of
// "%R~beam:bw_limit", so a Contains check passes either way and would not have
// caught the grant this test exists for.
func TestNodeGlobalKeysAreReadOnlyWhereTheNodeOnlyReads(t *testing.T) {
	rules := BuildNodeACLRules("n1", "pw", []string{"uuid-a"})
	tokens := make(map[string]bool, len(rules))
	for _, v := range rules {
		tokens[v.(string)] = true
	}

	// node/main.go and node/beam_throttle.go read every one of these with
	// rdb.Get and write none of them.
	readOnly := []string{
		"dylaris:routing_mode",
		"dylaris:file_access_mode",
		"beam:bw_limit",
		"beam:bw_up_internal",
		"beam:bw_down_internal",
	}
	for _, k := range readOnly {
		if tokens["~"+k] {
			t.Errorf("%s is granted read+write; the node only reads it, and it is global", k)
		}
		if !tokens["%R~"+k] {
			t.Errorf("%s is missing its read-only grant (%%R~%s)", k, k)
		}
	}

	// The counterpart: keys the node genuinely writes must stay read+write, so a
	// blanket tightening cannot pass this test either.
	for _, k := range []string{"dylaris:migration:*", "dylaris:beam:daily:*"} {
		if !tokens["~"+k] {
			t.Errorf("%s must stay read+write - the node writes it", k)
		}
	}
}

func TestBuildShipperACLRulesIsNarrow(t *testing.T) {
	r := joinRules(BuildShipperACLRules("pw", []string{"uuid-a"}))
	if !strings.Contains(r, "~dylaris:server:uuid-a:*") {
		t.Error("shipper must allow its server keys")
	}
	for _, forbidden := range []string{"dylaris:node:", "dylaris:core:", "sftp:auth", ":cmds"} {
		if strings.Contains(r, forbidden) {
			t.Errorf("shipper rules must NOT contain %q: %s", forbidden, r)
		}
	}
}
