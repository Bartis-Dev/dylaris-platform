package database

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"dylaris-core/authz"
)

// panelRoleSeed is one default system panel role (phase 2).
type panelRoleSeed struct {
	Name         string
	Capabilities []string
}

// supportPanelCaps is the pinned capability preset for the seeded 'support'
// system role: ticket handling plus read-only oversight. It DELIBERATELY
// excludes destructive/high-privilege panel caps (no users.delete, no plans.*,
// no settings.write, no *.write beyond tickets, no panelroles.*). Pinned from
// the PANEL scope of authz/catalog.go.
var supportPanelCaps = []string{
	"tickets.read",
	"tickets.write",
	"users.read",
	"servers.read",
	"audit.read",
}

// defaultPanelRoleSeeds returns the system panel roles seeded at boot. 'admin'
// carries every PANEL capability enumerated from the catalog (display and
// assignment completeness; the resolver still admin-short-circuits via the
// is_admin JWT claim, so this list is not what grants admin power). 'support'
// carries the pinned supportPanelCaps subset. Both are seeded is_system = TRUE
// and are therefore uneditable/undeletable via the management API.
func defaultPanelRoleSeeds() []panelRoleSeed {
	var adminCaps []string
	for _, c := range authz.ByScope(authz.ScopePanel) {
		adminCaps = append(adminCaps, c.ID)
	}
	return []panelRoleSeed{
		{Name: "admin", Capabilities: adminCaps},
		{Name: "support", Capabilities: supportPanelCaps},
	}
}

// seedDefaultPanelRoles inserts the default system panel roles idempotently.
// ON CONFLICT (name) DO NOTHING keeps an operator's same-named row intact and
// makes a re-boot a no-op. Runs on every boot from ensureSchema.
func seedDefaultPanelRoles(db *sql.DB) error {
	for _, seed := range defaultPanelRoleSeeds() {
		caps := seed.Capabilities
		if caps == nil {
			caps = []string{}
		}
		capsJSON, err := json.Marshal(caps)
		if err != nil {
			return fmt.Errorf("seed panel role %q: marshal caps: %w", seed.Name, err)
		}
		if _, err := db.Exec(
			`INSERT INTO panel_roles (name, capabilities, is_system) VALUES ($1, $2::jsonb, TRUE) ON CONFLICT (name) DO NOTHING`,
			seed.Name, string(capsJSON)); err != nil {
			return fmt.Errorf("seed panel role %q: %w", seed.Name, err)
		}
	}
	return nil
}

// backfillPanelRoleAssignments is a one-time, idempotent, forward-only mapping
// of the legacy users.role column onto users.panel_role_id: role 'admin' -> the
// seeded admin role, 'support' -> the seeded support role. role 'user' stays
// NULL (ordinary end-user, no staff powers) and needs no statement. The
// `panel_role_id IS NULL` guard makes a re-boot a no-op and never clobbers a
// role assigned via the management API. The legacy role/is_admin/can_* columns
// are LEFT IN PLACE (dropped in a later phase). MUST run after
// seedDefaultPanelRoles so the target rows exist.
func backfillPanelRoleAssignments(db *sql.DB) error {
	for _, m := range []struct{ role, roleName string }{
		{"admin", "admin"},
		{"support", "support"},
	} {
		if _, err := db.Exec(
			`UPDATE users SET panel_role_id = (SELECT id FROM panel_roles WHERE name = $1 AND is_system) WHERE role = $2 AND panel_role_id IS NULL`,
			m.roleName, m.role); err != nil {
			return fmt.Errorf("backfill panel_role_id for role %q: %w", m.role, err)
		}
	}
	return nil
}
