package database

import (
	"database/sql"
	"fmt"
)

// applyPhase15Schema sets up the User UUID migration + username
// history tables. The users.id UUID + drop of public_id happen in
// createUsersTable; this function only adds the new history table +
// settings rows. Idempotent.
func applyPhase15Schema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_username_history (
		id           SERIAL PRIMARY KEY,
		user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		old_username VARCHAR(64) NOT NULL,
		new_username VARCHAR(64) NOT NULL,
		changed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		changed_by   UUID REFERENCES users(id) ON DELETE SET NULL
	)`); err != nil {
		return fmt.Errorf("phase 15: create user_username_history: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_username_history_user
		ON user_username_history(user_id, changed_at DESC)`); err != nil {
		return fmt.Errorf("phase 15: create username_history index: %w", err)
	}
	for _, q := range []string{
		`INSERT INTO settings (key, value) VALUES ('users.allow_name_change', 'true') ON CONFLICT (key) DO NOTHING`,
		`INSERT INTO settings (key, value) VALUES ('users.name_change_cooldown_days', '30') ON CONFLICT (key) DO NOTHING`,
	} {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("phase 15: seed settings: %w", err)
		}
	}
	return nil
}

// applyPhase16Schema sets up the Modpack Storage + Feature Toggle schema:
//   - users.can_create_modpacks per-user gate (default true)
//   - modpack_versions gets frozen + mrpack_sha256 columns
//   - mrpack_storage_path renamed to mrpack_storage_key (semantic: relative
//     key under provider root, not a filesystem path)
//   - Default settings rows for feature toggle + storage config
//
// Idempotent. Schema is dev-fresh on first boot; the RENAME catches the case
// where an older build wrote the old column name and this one swaps it.
func applyPhase16Schema(db *sql.DB) error {
	// users.can_create_modpacks
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS can_create_modpacks BOOLEAN NOT NULL DEFAULT TRUE`); err != nil {
		return fmt.Errorf("phase 16: add can_create_modpacks: %w", err)
	}
	// Rename mrpack_storage_path → mrpack_storage_key if the old name exists.
	// Postgres has no "ALTER TABLE ... RENAME COLUMN IF EXISTS"; we probe and
	// guard the rename instead.
	var has bool
	_ = db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema='public' AND table_name='modpack_versions' AND column_name='mrpack_storage_path')`).Scan(&has)
	if has {
		if _, err := db.Exec(`ALTER TABLE modpack_versions RENAME COLUMN mrpack_storage_path TO mrpack_storage_key`); err != nil {
			return fmt.Errorf("phase 16: rename mrpack_storage_path: %w", err)
		}
	}
	// Add the new columns (idempotent via IF NOT EXISTS).
	if _, err := db.Exec(`ALTER TABLE modpack_versions ADD COLUMN IF NOT EXISTS mrpack_storage_key VARCHAR(512) NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("phase 16: add mrpack_storage_key: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE modpack_versions ADD COLUMN IF NOT EXISTS mrpack_sha256 VARCHAR(64) NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("phase 16: add mrpack_sha256: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE modpack_versions ADD COLUMN IF NOT EXISTS frozen BOOLEAN NOT NULL DEFAULT FALSE`); err != nil {
		return fmt.Errorf("phase 16: add frozen: %w", err)
	}
	// Seed default settings rows. ON CONFLICT keeps existing values on re-boot.
	for _, kv := range []struct{ k, v string }{
		{"feature_modpacks_enabled", "true"},
		{"modpack_storage_provider", "local"},
		{"modpack_storage_paths", "[]"},
		{"modpack_storage_s3_endpoint", ""},
		{"modpack_storage_s3_bucket", ""},
		{"modpack_storage_s3_region", ""},
		{"modpack_storage_s3_access_key", ""},
		{"modpack_storage_s3_secret_key", ""},
	} {
		if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO NOTHING`, kv.k, kv.v); err != nil {
			return fmt.Errorf("phase 16: seed settings: %w", err)
		}
	}
	return nil
}

// applyPhase14Schema sets up the Modpack Builder + Modrinth Publish schema:
//   - modpacks         : per-user authored modpacks
//   - modpack_versions : version history per pack (Draft/Beta/Release channels)
//   - modpack_mods     : Modrinth project+version refs per version
//   - modrinth_pats    : per-user Modrinth PAT (encrypted at rest)
//
// Slug uniqueness is per-user (different users may pick the same slug).
// Modrinth project/version IDs are populated only after first publish.
func applyPhase14Schema(db *sql.DB) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS modpacks (
			id                   SERIAL PRIMARY KEY,
			owner_id             UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name                 VARCHAR(128) NOT NULL,
			slug                 VARCHAR(128) NOT NULL,
			summary              VARCHAR(255) NOT NULL DEFAULT '',
			mc_version           VARCHAR(32)  NOT NULL DEFAULT '',
			loader               VARCHAR(32)  NOT NULL DEFAULT '',
			modrinth_project_id  VARCHAR(64)  NOT NULL DEFAULT '',
			modrinth_visibility  VARCHAR(16)  NOT NULL DEFAULT 'unlisted',
			created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			UNIQUE (owner_id, slug)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_modpacks_owner ON modpacks(owner_id, updated_at DESC)`,

		// Version-per-pack with channel column. Modrinth IDs are nullable
		// while the version is still a Draft (local).
		// mrpack_storage_path was renamed to mrpack_storage_key (the
		// column is a provider-relative key, not a filesystem path) and
		// frozen + mrpack_sha256 added for the cached-build model.
		`CREATE TABLE IF NOT EXISTS modpack_versions (
			id                  SERIAL PRIMARY KEY,
			modpack_id          INTEGER     NOT NULL REFERENCES modpacks(id) ON DELETE CASCADE,
			version_string      VARCHAR(64) NOT NULL,
			channel             VARCHAR(16) NOT NULL DEFAULT 'draft', -- draft|beta|release
			changelog           TEXT        NOT NULL DEFAULT '',
			mrpack_storage_key  VARCHAR(512) NOT NULL DEFAULT '',
			mrpack_sha256       VARCHAR(64) NOT NULL DEFAULT '',
			frozen              BOOLEAN     NOT NULL DEFAULT FALSE,
			file_size           BIGINT      NOT NULL DEFAULT 0,
			modrinth_version_id VARCHAR(64) NOT NULL DEFAULT '',
			created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			published_at        TIMESTAMPTZ,
			UNIQUE (modpack_id, version_string)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_modpack_versions_pack ON modpack_versions(modpack_id, created_at DESC)`,

		// Mods per version. side: 'client'|'server'|'both'. required=false
		// means the user explicitly marked it optional during build.
		`CREATE TABLE IF NOT EXISTS modpack_mods (
			id                       SERIAL PRIMARY KEY,
			modpack_version_id       INTEGER NOT NULL REFERENCES modpack_versions(id) ON DELETE CASCADE,
			modrinth_project_id      VARCHAR(64) NOT NULL,
			modrinth_project_slug    VARCHAR(128) NOT NULL DEFAULT '',
			modrinth_version_id      VARCHAR(64) NOT NULL,
			title                    VARCHAR(255) NOT NULL DEFAULT '',
			file_name                VARCHAR(255) NOT NULL DEFAULT '',
			download_url             TEXT NOT NULL DEFAULT '',
			sha512                   VARCHAR(128) NOT NULL DEFAULT '',
			side                     VARCHAR(8) NOT NULL DEFAULT 'both',
			required                 BOOLEAN NOT NULL DEFAULT TRUE,
			UNIQUE (modpack_version_id, modrinth_project_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_modpack_mods_version ON modpack_mods(modpack_version_id, title ASC)`,

		// PAT storage. ciphertext is hex(aes-gcm(plaintext)). nonce kept
		// inline (12 bytes prefix). One row per user — replacing the PAT
		// overwrites. Username + last_validated_at let the UI show
		// "Connected as <username> (valid as of ...)".
		`CREATE TABLE IF NOT EXISTS modrinth_pats (
			user_id           UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			ciphertext        TEXT        NOT NULL,
			modrinth_username VARCHAR(128) NOT NULL DEFAULT '',
			last_validated_at TIMESTAMPTZ,
			created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	} {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("phase 14: %w", err)
		}
	}
	return nil
}
