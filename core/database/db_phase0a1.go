package database

import (
	"database/sql"
	"fmt"
	"log"
)

// applyPhase0a1Schema sets up the Auth Foundation schema:
//   - new tables: regions, user_regions, audit_events_identity
//   - new columns on users (verification, reset, deletion tracking, all-regions flag)
//   - new column on servers (region)
//   - new columns on settings (updated_at, updated_by) for audit trail
//   - seeds the 'default' region
//   - backfills existing users with grandfathered email-verified + all-regions access
//   - normalizes empty nodes.region values to 'default'
//
// All operations are idempotent — safe at every boot and after schema-heal.
func applyPhase0a1Schema(db *sql.DB) error {
	// ---- New tables ----
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS regions (
		id           VARCHAR(32)  PRIMARY KEY,
		display_name VARCHAR(128) NOT NULL,
		enabled      BOOLEAN      NOT NULL DEFAULT TRUE,
		color        VARCHAR(16),
		created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("phase 0a.1: create regions: %w", err)
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_regions (
		user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		region_id VARCHAR(32) NOT NULL REFERENCES regions(id) ON DELETE CASCADE,
		PRIMARY KEY (user_id, region_id)
	)`); err != nil {
		return fmt.Errorf("phase 0a.1: create user_regions: %w", err)
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS audit_events_identity (
		id              BIGSERIAL PRIMARY KEY,
		event_type      VARCHAR(64) NOT NULL,
		actor_user_id   UUID REFERENCES users(id) ON DELETE SET NULL,
		target_user_id  UUID REFERENCES users(id) ON DELETE SET NULL,
		metadata        JSONB,
		ip_address      INET,
		user_agent      TEXT,
		created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("phase 0a.1: create audit_events_identity: %w", err)
	}
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_identity_target ON audit_events_identity(target_user_id, created_at DESC)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_identity_type   ON audit_events_identity(event_type, created_at DESC)`)

	// ---- Column extensions ----
	addCols := []struct{ table, col, def string }{
		{"users", "all_regions_access", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"users", "email_verified_at", "TIMESTAMPTZ"},
		{"users", "email_verification_token", "VARCHAR(64)"},
		{"users", "email_verification_sent_at", "TIMESTAMPTZ"},
		{"users", "password_reset_token", "VARCHAR(64)"},
		{"users", "password_reset_expires_at", "TIMESTAMPTZ"},
		{"users", "last_login_at", "TIMESTAMPTZ"},
		{"users", "deletion_status", "VARCHAR(32) NOT NULL DEFAULT 'active'"},
		{"users", "deletion_warning_sent_at", "TIMESTAMPTZ"},
		{"users", "deletion_scheduled_at", "TIMESTAMPTZ"},
		{"servers", "region", "VARCHAR(32) NOT NULL DEFAULT 'default'"},
		{"settings", "updated_at", "TIMESTAMPTZ DEFAULT NOW()"},
		{"settings", "updated_by", "UUID REFERENCES users(id) ON DELETE SET NULL"},
	}
	for _, c := range addCols {
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s", c.table, c.col, c.def)); err != nil {
			log.Printf("phase 0a.1: alter %s.%s: %v", c.table, c.col, err)
		}
	}

	// ---- Indexes ----
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_users_email_verify_token   ON users(email_verification_token) WHERE email_verification_token IS NOT NULL`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_users_password_reset_token ON users(password_reset_token)     WHERE password_reset_token IS NOT NULL`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_users_deletion_status      ON users(deletion_status)          WHERE deletion_status != 'active'`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_servers_region             ON servers(region)`)

	// ---- Seed default region (only if regions table is empty) ----
	db.Exec(`INSERT INTO regions (id, display_name, enabled)
		SELECT 'default', 'Default Region', TRUE
		WHERE NOT EXISTS (SELECT 1 FROM regions)`)

	// ---- Backfill existing data ----
	// Grandfather existing users as email-verified — there was no verification
	// system before this migration, so requiring it now would lock everyone out.
	db.Exec(`UPDATE users SET email_verified_at = created_at WHERE email_verified_at IS NULL`)
	// Preserve implicit access: any user that has no explicit region rows yet
	// gets all-regions access. New users created post-migration will get
	// explicit rows via the user-create flow.
	db.Exec(`UPDATE users SET all_regions_access = TRUE
		WHERE all_regions_access = FALSE
		  AND id NOT IN (SELECT user_id FROM user_regions)`)
	// Normalize blank node region (column predates the auth-foundation schema) to the seeded default.
	db.Exec(`UPDATE nodes SET region = 'default' WHERE region IS NULL OR region = ''`)

	return nil
}
