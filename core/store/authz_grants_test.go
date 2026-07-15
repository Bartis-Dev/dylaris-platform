package store

import (
	"encoding/json"
	"testing"
)

func TestCapOverridesJSONRoundTrip(t *testing.T) {
	in := CapOverrides{Grant: []string{"files.read", "files.write"}, Deny: []string{"files.delete"}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `{"grant":["files.read","files.write"],"deny":["files.delete"]}` {
		t.Fatalf("unexpected JSON: %s", b)
	}
	var out CapOverrides
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Grant) != 2 || out.Grant[0] != "files.read" || out.Deny[0] != "files.delete" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}

func TestCapOverridesEmptyObjectUnmarshals(t *testing.T) {
	// The '{}'::jsonb column default must unmarshal to an all-nil CapOverrides.
	var ov CapOverrides
	if err := json.Unmarshal([]byte(`{}`), &ov); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if ov.Grant != nil || ov.Deny != nil {
		t.Fatalf("empty object should yield nil slices, got %+v", ov)
	}
}

// TestPostgresStoreSatisfiesAuthzMethods is a compile-time assertion that the
// new read methods exist on *PostgresStore (the resolver depends on them).
func TestPostgresStoreSatisfiesAuthzMethods(t *testing.T) {
	var _ interface {
		GetPanelRole(id int) (*PanelRole, error)
		GetServerRole(id int) (*ServerRole, error)
		GetUserPanelAuthz(userID string) (*int, CapOverrides, error)
		GetServerGrant(serverID int, userID string) (*ServerGrant, error)
		GetAccountGrant(ownerUserID, userID string) (*ServerGrant, error)
	} = (*PostgresStore)(nil)
}
