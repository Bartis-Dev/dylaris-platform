package database

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"dylaris-core/authz"
)

// TestDefaultPanelRoleSeeds_Content pins the two system-role presets: admin
// carries EVERY panel cap (drift-proof, read from the catalog); support carries
// exactly the pinned subset and none of the destructive/high-privilege caps.
func TestDefaultPanelRoleSeeds_Content(t *testing.T) {
	seeds := defaultPanelRoleSeeds()
	if len(seeds) != 2 {
		t.Fatalf("got %d seeds, want 2", len(seeds))
	}
	byName := map[string][]string{}
	for _, s := range seeds {
		byName[s.Name] = s.Capabilities
	}

	adminCaps, ok := byName["admin"]
	if !ok {
		t.Fatal("missing admin seed")
	}
	adminSet := map[string]bool{}
	for _, c := range adminCaps {
		adminSet[c] = true
	}
	for _, c := range authz.ByScope(authz.ScopePanel) {
		if !adminSet[c.ID] {
			t.Errorf("admin seed missing panel cap %q", c.ID)
		}
	}

	supportCaps, ok := byName["support"]
	if !ok {
		t.Fatal("missing support seed")
	}
	want := map[string]bool{
		"tickets.read": true, "tickets.write": true,
		"users.read": true, "servers.read": true, "audit.read": true,
	}
	if len(supportCaps) != len(want) {
		t.Fatalf("support caps = %v, want %d entries", supportCaps, len(want))
	}
	for _, c := range supportCaps {
		if !want[c] {
			t.Errorf("unexpected support cap %q", c)
		}
		cap, ok := authz.Get(c)
		if !ok || cap.Scope != authz.ScopePanel {
			t.Errorf("support cap %q is not a real panel cap", c)
		}
	}
	supportSet := map[string]bool{}
	for _, c := range supportCaps {
		supportSet[c] = true
	}
	for _, forbidden := range []string{"users.delete", "plans.read", "plans.write", "settings.write", "panelroles.write"} {
		if supportSet[forbidden] {
			t.Errorf("support seed must not include %q", forbidden)
		}
	}
}

// TestSeedDefaultPanelRoles_Idempotent pins the idempotent INSERT shape: each
// system role is inserted with ON CONFLICT (name) DO NOTHING so a re-boot is a
// no-op at the DB level. Two seeds -> two execs (admin then support).
func TestSeedDefaultPanelRoles_Idempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	q := regexp.QuoteMeta(
		`INSERT INTO panel_roles (name, capabilities, is_system) VALUES ($1, $2::jsonb, TRUE) ON CONFLICT (name) DO NOTHING`)
	mock.ExpectExec(q).WithArgs("admin", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(q).WithArgs("support", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))

	if err := seedDefaultPanelRoles(db); err != nil {
		t.Fatalf("seedDefaultPanelRoles: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

// TestBackfillPanelRoleAssignments_GuardsExisting pins the forward-only backfill
// shape: each mapping UPDATE carries the `panel_role_id IS NULL` guard so it is
// idempotent and never clobbers a manually-assigned role.
func TestBackfillPanelRoleAssignments_GuardsExisting(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	q := regexp.QuoteMeta(
		`UPDATE users SET panel_role_id = (SELECT id FROM panel_roles WHERE name = $1 AND is_system) WHERE role = $2 AND panel_role_id IS NULL`)
	mock.ExpectExec(q).WithArgs("admin", "admin").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(q).WithArgs("support", "support").WillReturnResult(sqlmock.NewResult(0, 0))

	if err := backfillPanelRoleAssignments(db); err != nil {
		t.Fatalf("backfillPanelRoleAssignments: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
