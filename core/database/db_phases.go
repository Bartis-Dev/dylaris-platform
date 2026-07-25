package database

import (
	"database/sql"
	"fmt"
)

// applyPhase13Schema sets up the Custom Tabs schema:
//   - server_tabs: per-server user-defined tabs that render an external URL
//     in an iframe (Minimap, BlueMap, custom plugin dashboards). V1 holds
//     just the URL — auto-reverse-proxy via Gateway is a follow-up.
func applyPhase13Schema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS server_tabs (
		id             SERIAL PRIMARY KEY,
		server_id      INTEGER     NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		name           VARCHAR(64) NOT NULL,
		icon           VARCHAR(64) NOT NULL DEFAULT 'layout-grid',
		url            TEXT        NOT NULL,
		position       INTEGER     NOT NULL DEFAULT 0,
		enabled        BOOLEAN     NOT NULL DEFAULT TRUE,
		open_in_panel  BOOLEAN     NOT NULL DEFAULT TRUE,
		created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		created_by     UUID REFERENCES users(id) ON DELETE SET NULL
	)`); err != nil {
		return fmt.Errorf("phase 13: create server_tabs: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_server_tabs_server
		ON server_tabs(server_id, position ASC)`); err != nil {
		return fmt.Errorf("phase 13: create server_tabs index: %w", err)
	}
	return nil
}

// applyWS5TabProxySchema extends the Custom Tabs schema (WS5) with the
// reverse-proxy columns. All columns are nullable or defaulted so existing
// `direct` rows are untouched. share_token is UNIQUE where present so a
// tokenized `/c/<token>` slug can never collide. Idempotent.
func applyWS5TabProxySchema(db *sql.DB) error {
	for _, q := range []string{
		`ALTER TABLE server_tabs ADD COLUMN IF NOT EXISTS mode             VARCHAR(16)  NOT NULL DEFAULT 'direct'`,
		`ALTER TABLE server_tabs ADD COLUMN IF NOT EXISTS target_port      INTEGER`,
		`ALTER TABLE server_tabs ADD COLUMN IF NOT EXISTS target_path      TEXT         NOT NULL DEFAULT '/'`,
		`ALTER TABLE server_tabs ADD COLUMN IF NOT EXISTS surface          VARCHAR(16)  NOT NULL DEFAULT 'tab'`,
		`ALTER TABLE server_tabs ADD COLUMN IF NOT EXISTS visibility       VARCHAR(16)  NOT NULL DEFAULT 'private'`,
		`ALTER TABLE server_tabs ADD COLUMN IF NOT EXISTS share_token      VARCHAR(32)`,
		`ALTER TABLE server_tabs ADD COLUMN IF NOT EXISTS share_expires_at TIMESTAMPTZ`,
	} {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("ws5 tab proxy: alter server_tabs: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_server_tabs_share_token
		ON server_tabs(share_token) WHERE share_token IS NOT NULL`); err != nil {
		return fmt.Errorf("ws5 tab proxy: create share_token index: %w", err)
	}
	return nil
}

// applyPhase11Schema sets up the Spark Profiler schema:
//   - spark_profiles: profile-completion records keyed by spark.lucko.me URL
func applyPhase11Schema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS spark_profiles (
		id              SERIAL PRIMARY KEY,
		server_id       INTEGER     NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		sub_server_name VARCHAR(128) NOT NULL DEFAULT '',
		url             VARCHAR(512) NOT NULL,
		started_at      TIMESTAMPTZ,
		completed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		requested_by    UUID REFERENCES users(id) ON DELETE SET NULL,
		UNIQUE(server_id, url)
	)`); err != nil {
		return fmt.Errorf("phase 11: create spark_profiles: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_spark_profiles_server
		ON spark_profiles(server_id, completed_at DESC)`); err != nil {
		return fmt.Errorf("phase 11: create spark_profiles index: %w", err)
	}
	return nil
}

// applyPhase10Schema sets up the Modrinth Mod Browser schema:
//   - server_mods: tracks which Modrinth project/version is installed per
//     server+sub-server so "Update available" can compare installed vs latest
func applyPhase10Schema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS server_mods (
		id                     SERIAL PRIMARY KEY,
		server_id              INTEGER     NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		sub_server_name        VARCHAR(128) NOT NULL DEFAULT '',
		modrinth_project_id    VARCHAR(64) NOT NULL,
		modrinth_project_slug  VARCHAR(128) NOT NULL DEFAULT '',
		modrinth_version_id    VARCHAR(64) NOT NULL,
		title                  VARCHAR(255) NOT NULL DEFAULT '',
		file_name              VARCHAR(255) NOT NULL,
		sha512                 VARCHAR(128) NOT NULL DEFAULT '',
		installed_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		installed_by           UUID REFERENCES users(id) ON DELETE SET NULL,
		UNIQUE(server_id, sub_server_name, modrinth_project_id)
	)`); err != nil {
		return fmt.Errorf("phase 10: create server_mods: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_server_mods_server
		ON server_mods(server_id, sub_server_name)`); err != nil {
		return fmt.Errorf("phase 10: create server_mods index: %w", err)
	}
	return nil
}

// applyPhase9Schema sets up the RCON + API keys + Player Management schema:
//   - servers: rcon_enabled, rcon_port, rcon_password columns
//   - api_keys table for external RCON automation
//
// Idempotent. Wired from ensureSchema right after the scheduled-tasks schema.
func applyPhase9Schema(db *sql.DB) error {
	for _, q := range []string{
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS rcon_enabled       BOOLEAN     NOT NULL DEFAULT FALSE`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS rcon_port          INTEGER     NOT NULL DEFAULT 0`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS rcon_password      TEXT        NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS rcon_needs_restart BOOLEAN     NOT NULL DEFAULT FALSE`,
	} {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("phase 9: alter servers: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS api_keys (
		id             SERIAL PRIMARY KEY,
		user_id        UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name           VARCHAR(128) NOT NULL,
		key_hash       VARCHAR(64) NOT NULL UNIQUE,
		scope          JSONB        NOT NULL DEFAULT '{}'::jsonb,
		last_used_at   TIMESTAMPTZ,
		expires_at     TIMESTAMPTZ,
		revoked_at     TIMESTAMPTZ,
		rate_per_min   INTEGER      NOT NULL DEFAULT 60,
		created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("phase 9: create api_keys: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_api_keys_user
		ON api_keys(user_id, created_at DESC)`); err != nil {
		return fmt.Errorf("phase 9: create api_keys user index: %w", err)
	}
	return nil
}

// applyPhase8Schema sets up the Configuration Sub-Tabs + Scheduled Tasks schema:
//   - new table: scheduled_tasks (per-server cron jobs for restart + say)
//
// Idempotent. task_type is open-ended (varchar) so later work can introduce
// new types (e.g. rcon-command) without a schema migration.
func applyPhase8Schema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS scheduled_tasks (
		id             SERIAL PRIMARY KEY,
		server_id      INTEGER     NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		name           VARCHAR(128) NOT NULL DEFAULT '',
		task_type      VARCHAR(32)  NOT NULL,
		schedule_cron  VARCHAR(128) NOT NULL,
		payload        TEXT         NOT NULL DEFAULT '',
		enabled        BOOLEAN      NOT NULL DEFAULT TRUE,
		next_run       TIMESTAMPTZ,
		last_run       TIMESTAMPTZ,
		last_status    VARCHAR(32)  NOT NULL DEFAULT '',
		last_error     TEXT         NOT NULL DEFAULT '',
		created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
		created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
		updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("phase 8: create scheduled_tasks: %w", err)
	}
	// Hot path is "scan due tasks": leader-gated service ticks every 30s and
	// reads WHERE enabled AND next_run <= NOW(). Partial index keeps the
	// scan cheap even at thousands of tasks.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_due
		ON scheduled_tasks(next_run)
		WHERE enabled = TRUE AND next_run IS NOT NULL`); err != nil {
		return fmt.Errorf("phase 8: create scheduled_tasks due index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_server
		ON scheduled_tasks(server_id, created_at DESC)`); err != nil {
		return fmt.Errorf("phase 8: create scheduled_tasks server index: %w", err)
	}
	return nil
}

// applyPhase18Schema seeds the Telemetry Heartbeat toggle for
// anonymous usage stats that the website's live counter consumes. Default
// is ENABLED — operators are informed at boot + in Settings → Features and
// can opt out via the toggle or DYLARIS_TELEMETRY=false ENV.
func applyPhase18Schema(db *sql.DB) error {
	for _, kv := range []struct{ k, v string }{
		{"telemetry_enabled", "true"},
		{"telemetry_endpoint", "https://dylaris.dev/api/heartbeat"},
	} {
		if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO NOTHING`, kv.k, kv.v); err != nil {
			return fmt.Errorf("phase 18: seed %s: %w", kv.k, err)
		}
	}
	return nil
}

// applyPhase17Schema seeds the two First-Run Setup Wizard
// settings rows used by the wizard + admin-reset ENV. Both default to empty
// strings.
func applyPhase17Schema(db *sql.DB) error {
	for _, k := range []string{"setup_recovery_token", "last_admin_reset_nonce"} {
		if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES ($1, '')
			ON CONFLICT (key) DO NOTHING`, k); err != nil {
			return fmt.Errorf("phase 17: seed %s: %w", k, err)
		}
	}
	return nil
}

// applyModpackCrosscheckSchema sets up the modpack cross-check snapshot:
//   - server_modpack_contents: per-server-per-sub-server snapshot of the
//     modpack's Modrinth-identified members, captured at install/reinstall.
//     Sibling of server_mods; drives the advisory Content-tab cross-check.
//
// Idempotent. Only Modrinth-identified members are ever inserted (a pack member
// with no project id is skipped by the snapshot writer, not stored here).
func applyModpackCrosscheckSchema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS server_modpack_contents (
		id                       SERIAL PRIMARY KEY,
		server_id                INTEGER     NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		sub_server_name          TEXT        NOT NULL,
		modrinth_project_id      VARCHAR(64) NOT NULL,
		modrinth_version_id      VARCHAR(64) NOT NULL,
		modrinth_version_number  VARCHAR(64) NOT NULL DEFAULT '',
		file_name                TEXT        NOT NULL DEFAULT '',
		side                     VARCHAR(8)  NOT NULL DEFAULT 'both',
		UNIQUE (server_id, sub_server_name, modrinth_project_id)
	)`); err != nil {
		return fmt.Errorf("modpack crosscheck: create server_modpack_contents: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_server_modpack_contents_server
		ON server_modpack_contents(server_id, sub_server_name)`); err != nil {
		return fmt.Errorf("modpack crosscheck: create server_modpack_contents index: %w", err)
	}
	return nil
}
