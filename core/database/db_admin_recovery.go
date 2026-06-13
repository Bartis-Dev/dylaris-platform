package database

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"strings"
)

// applyAdminResetEnvIfRequested — Admin Recovery
//
// If the operator sets DYLARIS_RESET_ADMINS=<any-unique-nonce>, this:
//  1. Demotes every admin (is_admin=true) to role=user, wipes their TOTP
//  2. Writes audit_events_identity rows (event_type=admin.demoted_via_env)
//     so the demotion is tracked
//  3. Persists the nonce in settings.last_admin_reset_nonce so a stale
//     env var on the NEXT boot is detected as a no-op + warning, instead
//     of re-nuking a freshly-created admin
//
// To reset again after a successful reset: change the ENV value to a new
// nonce (e.g. today's date + a suffix) — anything different from the stored
// nonce triggers a fresh reset.
func applyAdminResetEnvIfRequested(db *sql.DB) {
	nonce := os.Getenv("DYLARIS_RESET_ADMINS")
	if nonce == "" {
		return
	}
	var last string
	_ = db.QueryRow(`SELECT value FROM settings WHERE key = 'last_admin_reset_nonce'`).Scan(&last)
	if last == nonce {
		log.Printf("[ADMIN-RESET] ENV still set after previous reset (nonce: %s). Skipping. Unset DYLARIS_RESET_ADMINS or change its value to reset again.", nonce)
		return
	}

	// Capture admin IDs first so we can audit-log each demotion.
	rows, err := db.Query(`SELECT id, username FROM users WHERE is_admin = true`)
	if err != nil {
		log.Printf("[ADMIN-RESET] failed to read admins: %v", err)
		return
	}
	type adminRow struct{ ID, Username string }
	var admins []adminRow
	for rows.Next() {
		var a adminRow
		if err := rows.Scan(&a.ID, &a.Username); err == nil {
			admins = append(admins, a)
		}
	}
	rows.Close()

	if _, err := db.Exec(`UPDATE users SET is_admin = false, role = 'user', totp_secret = '' WHERE is_admin = true`); err != nil {
		log.Printf("[ADMIN-RESET] demote failed: %v", err)
		return
	}

	// Persist the nonce — ON CONFLICT in case the row exists with the empty
	// string from applyPhase17Schema.
	if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES ('last_admin_reset_nonce', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, nonce); err != nil {
		log.Printf("[ADMIN-RESET] failed to persist nonce: %v", err)
	}

	// Write per-admin audit rows. Best-effort — actor_user_id stays NULL for
	// system-originated demotions (audit_events_identity has no plain-text
	// actor column; the metadata.reason carries the cause).
	metaJSON, _ := json.Marshal(map[string]interface{}{"nonce": nonce, "reason": "admin_reset_env"})
	for _, a := range admins {
		_, _ = db.Exec(`INSERT INTO audit_events_identity (event_type, actor_user_id, target_user_id, metadata, created_at)
			VALUES ('admin.demoted_via_env', NULL, $1, $2::jsonb, NOW())`, a.ID, string(metaJSON))
	}

	names := make([]string, len(admins))
	for i, a := range admins {
		names[i] = a.Username
	}
	log.Printf("[ADMIN-RESET] Demoted %d admin(s) (%s) and wiped their 2FA.", len(admins), strings.Join(names, ", "))
	log.Printf("[ADMIN-RESET] Platform now in Lost-Admin Mode. Watch logs for Recovery Token.")
	log.Printf("[ADMIN-RESET] UNSET DYLARIS_RESET_ADMINS now (or restart will skip but warn).")
}
