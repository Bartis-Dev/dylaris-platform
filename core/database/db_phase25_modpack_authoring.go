package database

import (
	"database/sql"
	"fmt"
)

// applyModpackAuthoringSchema splits the single modpack toggle into two and adds
// the manual-override marker the split needs.
//
// Before this there was ONE setting, feature_modpacks_enabled, written by two
// different admin screens, and one per-user column, can_create_modpacks, that
// defaulted to TRUE. So "modpacks on" meant "everyone who holds modpack.write
// may author", with no way to say "admins only".
//
//   - feature_modpack_authoring_enabled: end-user authoring. With modpacks on and
//     this off, only admins author.
//   - users.can_create_modpacks_manual: TRUE once an admin has set that user's
//     flag by hand. A bulk apply driven by the authoring toggle can then skip
//     those rows, so a deliberate per-user decision is not silently reverted the
//     next time the global switch moves. Same distinction the authz layer already
//     draws between a role default and panel_cap_overrides.
//
// Additive + idempotent. Defaults are chosen so an existing install does not
// change behaviour on boot: authoring off matches "the admin has not opted in
// yet", and no existing row is marked manual (nothing was tracked before, so
// claiming otherwise would freeze rows that were never deliberate).
func applyModpackAuthoringSchema(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS can_create_modpacks_manual BOOLEAN NOT NULL DEFAULT FALSE`); err != nil {
		return fmt.Errorf("modpack authoring: add users.can_create_modpacks_manual: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES ('feature_modpack_authoring_enabled', 'false')
		ON CONFLICT (key) DO NOTHING`); err != nil {
		return fmt.Errorf("modpack authoring: seed feature_modpack_authoring_enabled: %w", err)
	}

	// The /modpacks page existed but had no navbar entry, because there was no
	// modules row for it (seedSystemModules even deletes a legacy SYSTEM row of
	// that name - this one is deliberately non-system, so it survives that).
	//
	// Both derived columns are seeded from the CURRENT settings rather than
	// hardcoded, so an install that already had modpacks on gets a working entry
	// on this boot instead of an off row nobody would think to enable. From here
	// on, syncModpackModule keeps them in step whenever the flags move.
	if _, err := db.Exec(`INSERT INTO modules (name, type, icon, url, is_enabled, is_system, position, access_role)
		SELECT 'Modpacks', 'internal', 'package', '/modpacks',
		       COALESCE((SELECT value = 'true' FROM settings WHERE key = 'feature_modpacks_enabled'), FALSE),
		       FALSE, 6,
		       CASE WHEN COALESCE((SELECT value = 'true' FROM settings WHERE key = 'feature_modpack_authoring_enabled'), FALSE)
		            THEN 'all' ELSE 'admin' END
		WHERE NOT EXISTS (SELECT 1 FROM modules WHERE name = 'Modpacks')`); err != nil {
		return fmt.Errorf("modpack authoring: seed Modpacks module row: %w", err)
	}

	// Re-derive the row on every boot, not only when the Features form is saved.
	//
	// is_enabled and access_role are declared derived (see syncModpackModule) but
	// were only ever computed at write time, so any row that got out of step
	// stayed out of step forever: nothing re-reads the flags unless an admin
	// happens to save Features again. An install that gained this row while
	// authoring was still off therefore kept showing "Admin" in Settings ->
	// Modules - and stayed hidden from users - long after authoring was opened.
	//
	// url is repaired here too: the navbar routes internal modules by that column,
	// so an empty one silently sends Modpacks to the generic /modules/<id>
	// placeholder instead of the real page. position and the row's existence are
	// left alone - those are the admin's.
	if _, err := db.Exec(`UPDATE modules SET
			url = '/modpacks',
			is_enabled = COALESCE((SELECT value = 'true' FROM settings WHERE key = 'feature_modpacks_enabled'), FALSE),
			access_role = CASE WHEN COALESCE((SELECT value = 'true' FROM settings WHERE key = 'feature_modpacks_enabled'), FALSE)
			                    AND COALESCE((SELECT value = 'true' FROM settings WHERE key = 'feature_modpack_authoring_enabled'), FALSE)
			                   THEN 'all' ELSE 'admin' END
		WHERE name = 'Modpacks'`); err != nil {
		return fmt.Errorf("modpack authoring: re-derive Modpacks module row: %w", err)
	}
	return nil
}
