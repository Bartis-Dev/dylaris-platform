package database

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"dylaris-core/models"
	"dylaris-core/store"
)

// mapLegacyInviteCaps maps the legacy 9-bool TabPermissions blob to the new
// granular SERVER cap set (phase 3 migration). Field order is fixed so the
// output is deterministic. inherit is NOT a cap (it stays the invite.inherit
// column). There was no legacy backups bit, so a migrated friend gets NO
// backups.* caps - this preserves the old behavior where backups were
// owner/admin only. Every id here is a real ScopeServer cap in authz/catalog.go
// (enforced by db_phase21_invite_migration_test.go).
func mapLegacyInviteCaps(p models.TabPermissions) []string {
	caps := []string{}
	if p.Console {
		caps = append(caps, "console.read", "console.send")
	}
	if p.Files {
		caps = append(caps, "files.read", "files.write", "files.delete")
	}
	if p.Config {
		caps = append(caps, "config.read", "config.write", "mods.read", "mods.write", "mods.delete")
	}
	if p.Power {
		caps = append(caps, "power.start", "power.stop", "power.restart", "power.kill", "rcon.exec", "players.read", "players.manage")
	}
	if p.Network {
		caps = append(caps, "network.read", "network.write")
	}
	if p.Overview {
		caps = append(caps, "overview.read")
	}
	if p.Members {
		caps = append(caps, "members.read", "members.write", "members.delete")
	}
	if p.Setup {
		caps = append(caps, "server.settings.write")
	}
	return caps
}

// migrateLegacyServerInvites is a one-time, idempotent, forward-only, ADDITIVE
// migration of every legacy server_invites row onto the reworked granular
// columns. For each un-migrated row it writes cap_overrides.Grant =
// mapLegacyInviteCaps(blob), copies the blob's inherit bit into the inherit
// column, and derives owner_user_id from the server's owner. The OLD
// permissions column is LEFT INTACT (dropped in a later phase). The guard
// (server_id IS NOT NULL AND owner_user_id IS NULL AND cap_overrides = '{}')
// makes a re-boot a no-op: after migration owner_user_id is non-NULL. MUST run
// before phase 4 flips the server routes strict so migrated friends keep
// access. Rows are collected before updating to avoid iterating a live cursor
// while writing on the pool.
func migrateLegacyServerInvites(db *sql.DB) error {
	rows, err := db.Query(
		`SELECT id, COALESCE(permissions, '{}'::jsonb) FROM server_invites WHERE server_id IS NOT NULL AND owner_user_id IS NULL AND cap_overrides = '{}'::jsonb`)
	if err != nil {
		return fmt.Errorf("migrate legacy invites: select: %w", err)
	}
	type pending struct {
		id      int
		caps    []string
		inherit bool
	}
	var todo []pending
	for rows.Next() {
		var id int
		var permJSON []byte
		if err := rows.Scan(&id, &permJSON); err != nil {
			rows.Close()
			return fmt.Errorf("migrate legacy invites: scan: %w", err)
		}
		var p models.TabPermissions
		_ = json.Unmarshal(permJSON, &p)
		todo = append(todo, pending{id: id, caps: mapLegacyInviteCaps(p), inherit: p.Inherit})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("migrate legacy invites: rows: %w", err)
	}
	rows.Close()

	for _, t := range todo {
		ovJSON, err := json.Marshal(store.CapOverrides{Grant: t.caps})
		if err != nil {
			return fmt.Errorf("migrate legacy invite %d: marshal caps: %w", t.id, err)
		}
		if _, err := db.Exec(
			`UPDATE server_invites SET cap_overrides = $1::jsonb, inherit = $2, owner_user_id = (SELECT owner_id FROM servers WHERE servers.id = server_invites.server_id) WHERE id = $3`,
			string(ovJSON), t.inherit, t.id); err != nil {
			return fmt.Errorf("migrate legacy invite %d: update: %w", t.id, err)
		}
	}
	return nil
}
