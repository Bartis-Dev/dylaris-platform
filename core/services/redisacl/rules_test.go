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
