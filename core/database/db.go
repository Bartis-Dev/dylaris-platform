package database

import (
	"database/sql"
	"dylaris-core/config"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq" // Required: Postgres Driver
)

func InitDB(cfg config.Config) (*sql.DB, error) {
	// Postgres Connection String (DSN)
	sslMode := cfg.DBSSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, sslMode)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("DB Open Error: %w", err)
	}

	// Wait for Postgres to accept connections instead of crashing out —
	// covers the Core booting before the DB in a compose / stack deploy.
	if err := pingWithRetry(db, 60, 2*time.Second); err != nil {
		return nil, fmt.Errorf("DB Ping Error: %w", err)
	}
	log.Println("Postgres DB connection established.")

	if err := ensureSchema(db); err != nil {
		return nil, err
	}

	return db, nil
}

// pingWithRetry blocks until the database answers or the attempt budget
// is exhausted.
func pingWithRetry(db *sql.DB, attempts int, delay time.Duration) error {
	var err error
	for i := 1; i <= attempts; i++ {
		if err = db.Ping(); err == nil {
			return nil
		}
		log.Printf("DB not ready (%d/%d): %v", i, attempts, err)
		time.Sleep(delay)
	}
	return err
}

// ensureSchema creates every table, applies column migrations and seeds
// the baseline rows. Every statement is idempotent (CREATE/ALTER ... IF
// NOT EXISTS, conditional inserts), so it is safe to run repeatedly.
func ensureSchema(db *sql.DB) error {
	if err := createUsersTable(db); err != nil {
		return err
	}
	if err := createModulesTable(db); err != nil {
		return err
	}
	if err := createNodesTable(db); err != nil {
		return err
	}
	if err := createServersTable(db); err != nil {
		return err
	}
	if err := createSettingsTable(db); err != nil {
		return err
	}
	if err := createServerInvitesTable(db); err != nil {
		return err
	}
	if err := createServerStatsTable(db); err != nil {
		return err
	}
	if err := createGatewayTables(db); err != nil {
		return err
	}
	if err := createLibraryDisabledTable(db); err != nil {
		return err
	}
	if err := createBackupTables(db); err != nil {
		return err
	}
	if err := createTicketTables(db); err != nil {
		return err
	}
	if err := migrateSchema(db); err != nil {
		return err
	}
	if err := applyPhase0a1Schema(db); err != nil {
		return err
	}
	if err := applyPhase8Schema(db); err != nil {
		return err
	}
	if err := applyPhase9Schema(db); err != nil {
		return err
	}
	if err := applyPhase10Schema(db); err != nil {
		return err
	}
	if err := applyPhase13Schema(db); err != nil {
		return err
	}
	if err := applyPhase14Schema(db); err != nil {
		return err
	}
	if err := applyPhase11Schema(db); err != nil {
		return err
	}
	if err := applyPhase15Schema(db); err != nil {
		return err
	}
	if err := applyPhase16Schema(db); err != nil {
		return err
	}
	if err := applyPhase17Schema(db); err != nil {
		return err
	}
	if err := applyPhase18Schema(db); err != nil {
		return err
	}
	if err := applyWarpSchema(db); err != nil {
		return err
	}
	applyAdminResetEnvIfRequested(db)

	seedSystemModules(db)
	return nil
}

// createTicketTables (Phase 2) sets up the ticket-system schema: categories,
// tickets themselves, messages on those tickets, watchers (CC), and per-ticket
// audit events. The region column is on tickets from day one so a future
// cross-region migration doesn't have to rewrite history.
func createTicketTables(db *sql.DB) error {
	tables := []string{
		// Categories: admin-curated, users pick one when creating a ticket.
		// requires_server gates the server picker on the create form.
		// default_priority pre-seeds the priority dropdown.
		// default_assignee_team is the team string used as fallback assignee.
		`CREATE TABLE IF NOT EXISTS ticket_categories (
			id                    SERIAL PRIMARY KEY,
			name                  VARCHAR(128) NOT NULL,
			description           TEXT NOT NULL DEFAULT '',
			requires_server       BOOLEAN NOT NULL DEFAULT FALSE,
			default_priority      VARCHAR(16) NOT NULL DEFAULT 'normal',
			default_assignee_team VARCHAR(64),
			color                 VARCHAR(16),
			enabled               BOOLEAN NOT NULL DEFAULT TRUE,
			position              INTEGER NOT NULL DEFAULT 0,
			created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(name)
		)`,
		// Tickets. server_uuid + server_region are nullable — only set when
		// the category required a server. assigned_user_id is the supporter
		// owning the ticket; assigned_team scopes visibility per Phase 1
		// support_team semantics.
		`CREATE TABLE IF NOT EXISTS tickets (
			id               SERIAL PRIMARY KEY,
			region           VARCHAR(32) NOT NULL DEFAULT 'default',
			category_id      INTEGER NOT NULL REFERENCES ticket_categories(id) ON DELETE RESTRICT,
			user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			server_uuid      VARCHAR(64),
			server_region    VARCHAR(32),
			title            VARCHAR(200) NOT NULL,
			status           VARCHAR(32) NOT NULL DEFAULT 'open',
			priority         VARCHAR(16) NOT NULL DEFAULT 'normal',
			assigned_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			assigned_team    VARCHAR(64),
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			closed_at        TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tickets_user        ON tickets(user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tickets_status      ON tickets(status, priority, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tickets_assignee    ON tickets(assigned_user_id) WHERE assigned_user_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_tickets_team        ON tickets(assigned_team) WHERE assigned_team IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_tickets_server      ON tickets(server_uuid) WHERE server_uuid IS NOT NULL`,

		// Messages on a ticket. is_internal hides the message from the
		// ticket creator + watchers — only visible to support+admin.
		`CREATE TABLE IF NOT EXISTS ticket_messages (
			id          SERIAL PRIMARY KEY,
			ticket_id   INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
			user_id     UUID NOT NULL REFERENCES users(id) ON DELETE SET NULL,
			body        TEXT NOT NULL,
			is_internal BOOLEAN NOT NULL DEFAULT FALSE,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket ON ticket_messages(ticket_id, created_at ASC)`,

		// Watchers / CC. can_reply distinguishes "read-only" from
		// "co-resolves with me" — admin sets the policy default.
		`CREATE TABLE IF NOT EXISTS ticket_watchers (
			ticket_id  INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
			user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			can_reply  BOOLEAN NOT NULL DEFAULT FALSE,
			added_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			added_by   UUID REFERENCES users(id) ON DELETE SET NULL,
			PRIMARY KEY (ticket_id, user_id)
		)`,

		// Per-ticket audit. Separate from audit_events_identity since the
		// audience and retention concerns differ (ticket audit is shown in
		// the UI to support+admin; identity audit is admin-only ops history).
		`CREATE TABLE IF NOT EXISTS ticket_audit_events (
			id            BIGSERIAL PRIMARY KEY,
			ticket_id     INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
			event_type    VARCHAR(64) NOT NULL,
			actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			metadata      JSONB,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ticket_audit_ticket ON ticket_audit_events(ticket_id, created_at DESC)`,

		// Phase 3 — attachments. Storage layer holds the actual bytes;
		// this table is just metadata + the storage key the provider uses
		// to retrieve. message_id is nullable so attachments can be
		// uploaded into the create-ticket form before any messages exist.
		`CREATE TABLE IF NOT EXISTS ticket_attachments (
			id          SERIAL PRIMARY KEY,
			ticket_id   INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
			message_id  INTEGER REFERENCES ticket_messages(id) ON DELETE SET NULL,
			filename    VARCHAR(255) NOT NULL,
			mime        VARCHAR(128) NOT NULL DEFAULT 'application/octet-stream',
			size_bytes  BIGINT NOT NULL DEFAULT 0,
			storage_key VARCHAR(512) NOT NULL,
			uploaded_by UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ticket_attachments_ticket ON ticket_attachments(ticket_id, created_at ASC)`,

		// Phase 3 — canned responses. Admin-managed snippets that support
		// staff insert into replies. category_id ties a snippet to a
		// specific category for discoverability (NULL = global).
		// body supports variable expansion at insert-time on the client:
		// {{user_name}}, {{ticket_id}}, {{server_name}}, {{actor_name}}.
		`CREATE TABLE IF NOT EXISTS ticket_canned_responses (
			id          SERIAL PRIMARY KEY,
			name        VARCHAR(128) NOT NULL,
			body        TEXT NOT NULL,
			category_id INTEGER REFERENCES ticket_categories(id) ON DELETE SET NULL,
			created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_canned_category ON ticket_canned_responses(category_id) WHERE category_id IS NOT NULL`,

		// Phase 3 — notifications. Generic in-app inbox; ticket events are
		// the first producer but the schema is intentionally open-ended
		// so other systems (backups, maintenance, security questions)
		// can write here later.
		`CREATE TABLE IF NOT EXISTS notifications (
			id         BIGSERIAL PRIMARY KEY,
			user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			type       VARCHAR(64) NOT NULL,
			title      VARCHAR(200) NOT NULL,
			body       TEXT NOT NULL DEFAULT '',
			link       VARCHAR(500),
			read_at    TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_user_unread ON notifications(user_id, created_at DESC) WHERE read_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_user        ON notifications(user_id, created_at DESC)`,

		// Phase 4 — per-server audit. Indexed by (server_id, created_at)
		// since the dominant query is "events for one server, newest first".
		// Index on event_type lets the future filter dropdown stay fast.
		// metadata stays JSONB so producers can shape it freely.
		`CREATE TABLE IF NOT EXISTS server_audit_events (
			id             BIGSERIAL PRIMARY KEY,
			server_id      INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			region         VARCHAR(32) NOT NULL DEFAULT 'default',
			event_type     VARCHAR(64) NOT NULL,
			actor_user_id  UUID REFERENCES users(id) ON DELETE SET NULL,
			target_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			metadata       JSONB,
			ip_address     INET,
			user_agent     TEXT,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_server_audit_server ON server_audit_events(server_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_server_audit_type   ON server_audit_events(event_type, created_at DESC)`,

		// ticket_deletions — append-only audit log for admin-driven ticket
		// deletions. Decoupled from tickets via no FK so the row survives the
		// referenced ticket disappearing. Username + category snapshots are
		// stored as text to stay readable after the source rows are gone.
		// deleted_by stays as UUID FK (SET NULL) so the actor's identity row
		// can be anonymized without nuking the audit history.
		`CREATE TABLE IF NOT EXISTS ticket_deletions (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			ticket_id       INTEGER NOT NULL,
			ticket_subject  TEXT NOT NULL,
			owner_user_id   UUID,
			owner_username  TEXT NOT NULL,
			category_name   TEXT,
			deleted_by      UUID NOT NULL,
			deleted_by_name TEXT NOT NULL,
			deleted_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			ip_address      INET,
			user_agent      TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ticket_deletions_deleted_at ON ticket_deletions (deleted_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_ticket_deletions_deleted_by ON ticket_deletions (deleted_by)`,
	}
	for _, q := range tables {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("create ticket tables: %w", err)
		}
	}
	return nil
}

// applyPhase0a1Schema sets up the Phase 0a.1 (Auth Foundation) schema:
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
	// Normalize blank node region (column existed pre-Phase-0a.1) to the seeded default.
	db.Exec(`UPDATE nodes SET region = 'default' WHERE region IS NULL OR region = ''`)

	return nil
}

// applyPhase15Schema sets up the Phase 15 (User UUID migration + username
// history) tables. The users.id UUID + drop of public_id happen in
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

// applyPhase16Schema sets up Phase 16 (Modpack Storage + Feature Toggle):
//   - users.can_create_modpacks per-user gate (default true)
//   - modpack_versions gets frozen + mrpack_sha256 columns
//   - mrpack_storage_path renamed to mrpack_storage_key (semantic: relative
//     key under provider root, not a filesystem path)
//   - Default settings rows for feature toggle + storage config
//
// Idempotent. Schema is dev-fresh on first boot; the RENAME catches the case
// where Phase 14 wrote the old column name and Phase 16 swaps it.
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

// applyPhase14Schema sets up Phase 14 (Modpack Builder + Modrinth Publish):
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
		// Phase 16 renamed mrpack_storage_path → mrpack_storage_key (the
		// column is a provider-relative key, not a filesystem path) and
		// added frozen + mrpack_sha256 for the cached-build model.
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

// applyPhase13Schema sets up Phase 13 (Custom Tabs):
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

// applyPhase11Schema sets up Phase 11 (Spark Profiler):
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

// applyPhase10Schema sets up Phase 10 (Modrinth Mod Browser):
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

// applyPhase9Schema sets up Phase 9 (RCON + API keys + Player Management):
//   - servers: rcon_enabled, rcon_port, rcon_password columns
//   - api_keys table for external RCON automation
//
// Idempotent. Wired from ensureSchema right after applyPhase8Schema.
func applyPhase9Schema(db *sql.DB) error {
	for _, q := range []string{
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS rcon_enabled  BOOLEAN     NOT NULL DEFAULT FALSE`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS rcon_port     INTEGER     NOT NULL DEFAULT 0`,
		`ALTER TABLE servers ADD COLUMN IF NOT EXISTS rcon_password TEXT        NOT NULL DEFAULT ''`,
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

// applyPhase8Schema sets up Phase 8 (Configuration Sub-Tabs + Scheduled Tasks):
//   - new table: scheduled_tasks (per-server cron jobs for restart + say)
//
// Idempotent. task_type is open-ended (varchar) so later phases can introduce
// new types (e.g. rcon-command in P9) without a schema migration.
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

func createUsersTable(db *sql.DB) error {
	if _, err := db.Exec(`CREATE EXTENSION IF NOT EXISTS pgcrypto`); err != nil {
		return fmt.Errorf("create pgcrypto extension: %w", err)
	}
	query := `CREATE TABLE IF NOT EXISTS users (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		username TEXT NOT NULL UNIQUE,
		password TEXT NOT NULL,
		minecraft_username TEXT,
		email TEXT,
		is_admin BOOLEAN DEFAULT FALSE,
		is_2fa_enabled BOOLEAN DEFAULT FALSE,
		permissions TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_username_change TIMESTAMPTZ
	)`
	_, err := db.Exec(query)
	return err
}

func createModulesTable(db *sql.DB) error {
	// access_role gates which user role can see/use the module:
	//   "all"   — both admins and regular users (default for user-facing tabs)
	//   "admin" — admin-only (Admin, Infrastructure, Settings adjacent tabs)
	// Servers is hard-coded to "all" by seed and cannot be changed.
	query := `CREATE TABLE IF NOT EXISTS modules (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL,
		type TEXT NOT NULL,
		icon TEXT NOT NULL,
		url TEXT,
		is_enabled BOOLEAN DEFAULT TRUE,
		is_system BOOLEAN DEFAULT FALSE,
		position INTEGER DEFAULT 99,
		access_role TEXT DEFAULT 'all'
	)`
	_, err := db.Exec(query)
	return err
}

func createNodesTable(db *sql.DB) error {
	// Extended with Link settings
	query := `CREATE TABLE IF NOT EXISTS nodes (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL,
		address TEXT NOT NULL,
		token TEXT NOT NULL,
		status TEXT DEFAULT 'offline',
		is_local BOOLEAN DEFAULT FALSE,
		tags TEXT,
		link_enabled BOOLEAN DEFAULT FALSE,
		link_instances INTEGER DEFAULT 1,
		link_secret TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`
	_, err := db.Exec(query)
	return err
}

func createServersTable(db *sql.DB) error {
	query := `CREATE TABLE IF NOT EXISTS servers (
		id SERIAL PRIMARY KEY,
		uuid TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		node_id INTEGER NOT NULL REFERENCES nodes(id),
		owner_id UUID NOT NULL REFERENCES users(id),
		game_image TEXT,
		port INTEGER,
		memory INTEGER,
		start_command TEXT,
		status TEXT DEFAULT 'offline',
		is_fixed BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`
	_, err := db.Exec(query)
	return err
}

func createSettingsTable(db *sql.DB) error {
	query := `CREATE TABLE IF NOT EXISTS settings (
		key   VARCHAR(100) PRIMARY KEY,
		value TEXT
	)`
	_, err := db.Exec(query)
	return err
}

func createServerInvitesTable(db *sql.DB) error {
	query := `CREATE TABLE IF NOT EXISTS server_invites (
		id SERIAL PRIMARY KEY,
		server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		permissions JSONB DEFAULT '{}',
		invited_by UUID NOT NULL REFERENCES users(id),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(server_id, user_id)
	)`
	_, err := db.Exec(query)
	return err
}

func migrateSchema(db *sql.DB) error {
	cols := []struct{ table, col, def string }{
		{"users", "email", "TEXT"},
		{"users", "is_2fa_enabled", "BOOLEAN DEFAULT FALSE"},
		{"users", "totp_secret", "TEXT DEFAULT ''"},
		{"users", "totp_backup_codes", "JSONB DEFAULT '[]'"},
		{"users", "permissions", "TEXT"},
		{"nodes", "tags", "TEXT"},
		{"nodes", "link_enabled", "BOOLEAN DEFAULT FALSE"},
		{"nodes", "link_instances", "INTEGER DEFAULT 1"},
		{"nodes", "link_secret", "TEXT"},
		{"servers", "start_command", "TEXT"},
		{"servers", "is_fixed", "BOOLEAN DEFAULT TRUE"},
		{"servers", "active_sub_server", "TEXT DEFAULT ''"},
		{"servers", "extra_jvm_flags", "TEXT DEFAULT ''"},
		{"servers", "ports", "JSONB DEFAULT '[]'"},
		{"servers", "cpu_limit", "REAL DEFAULT 0"},
		{"nodes", "public_ip", "TEXT DEFAULT ''"},
		{"nodes", "private_ips", "JSONB DEFAULT '[]'"},
		{"servers", "installer_type", "TEXT DEFAULT ''"},
		{"servers", "minecraft_version", "TEXT DEFAULT ''"},
		{"servers", "build_number", "TEXT DEFAULT ''"},
		{"servers", "disk_limit", "BIGINT DEFAULT 0"},
		{"nodes", "cpuset_cpus", "TEXT DEFAULT ''"},
		{"servers", "desired_state", "TEXT DEFAULT 'stopped'"},
		{"servers", "server_type", "TEXT DEFAULT 'game'"},
		{"servers", "proxy_id", "INTEGER REFERENCES servers(id) ON DELETE SET NULL"},
		{"nodes", "link_image", "TEXT DEFAULT ''"},
		{"nodes", "last_seen_at", "TIMESTAMP DEFAULT CURRENT_TIMESTAMP"},
		{"servers", "host_port", "INT DEFAULT 0"},
		{"servers", "container_port", "INT DEFAULT 25565"},
		{"modules", "access_role", "TEXT DEFAULT 'all'"},
		// Auto-move: when true, the rebalance loop is allowed to migrate this
		// server to another node when the current node is overloaded.
		// Migration only happens while the server is stopped/idle.
		{"servers", "auto_move", "BOOLEAN DEFAULT FALSE"},
		// Per-node placement: overcommit ratios + observed capacity floor.
		// 1.0 = no overcommit, 1.5 = allow 50% over physical capacity.
		// total_* are last-known physical totals reported by the node's
		// heartbeat — cached so the scheduler does not need a live ping.
		{"nodes", "cpu_overcommit_ratio", "REAL DEFAULT 1.0"},
		{"nodes", "ram_overcommit_ratio", "REAL DEFAULT 1.0"},
		{"nodes", "total_cpu", "REAL DEFAULT 0"},
		{"nodes", "total_ram_mb", "BIGINT DEFAULT 0"},
		// Region key (e.g. "eu-central"). Set via DYLARIS_REGION on the node
		// and reported in the heartbeat. Treated as first-class metadata,
		// orthogonal to tags — tags describe capability/tier, region the
		// physical location for latency-based placement.
		{"nodes", "region", "TEXT DEFAULT ''"},
		// Phase 0a.5 — security questions. JSON array of
		// {question, answer_hash} pairs; answer_hash is bcrypt.
		{"users", "security_questions", "JSONB DEFAULT '[]'"},
		// Phase 4 — server audit. audit_enabled flips on automatically the
		// first time a non-owner is invited (saves space for solo-owner servers).
		// audit_force_on is the admin override for unconditional audit
		// (compliance setups). Effective gate is OR of the two.
		{"servers", "audit_enabled", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"servers", "audit_force_on", "BOOLEAN NOT NULL DEFAULT FALSE"},
		// Phase 1 — roles + granular capability flags.
		// role values: 'user' | 'support' | 'admin'. is_admin is kept as a
		// derived view for backward compat with handlers that read it
		// directly. support_team is used in Phase 2 (Tickets) to scope
		// ticket visibility — nullable for everyone else.
		{"users", "role", "VARCHAR(16) NOT NULL DEFAULT 'user'"},
		{"users", "can_delete_servers", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"users", "can_change_resources", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"users", "support_team", "VARCHAR(64)"},
	}
	for _, c := range cols {
		query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s", c.table, c.col, c.def)
		db.Exec(query)
	}

	// Unique constraints (idempotent)
	db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_name_unique ON nodes (name)`)

	// Phase 1 backfill: existing admins (is_admin=TRUE) get role='admin'
	// so the new role column matches their legacy capability. Idempotent —
	// users that already have a non-default role stay as-is.
	db.Exec(`UPDATE users SET role = 'admin' WHERE is_admin = TRUE AND role = 'user'`)

	return nil
}

// seedSystemModules inserts system modules that don't exist yet and migrates existing ones
func seedSystemModules(db *sql.DB) {
	modules := []struct {
		name, typ, icon, url, role string
		position                    int
		enabled, system             bool
	}{
		{"Servers", "internal", "server", "/servers", "all", 1, true, true},
		{"Admin", "internal", "shield-check", "/admin", "admin", 2, true, true},
		{"Infrastructure", "internal", "cpu", "/infrastructure", "admin", 3, true, true},
		{"Library", "internal", "folder-open", "/library", "admin", 4, false, false},
		// Phase 2 — Tickets module, default disabled. Admin opts in from
		// Settings → Modules. Once enabled it appears in the user-facing
		// sidebar via the standard module loader.
		{"Tickets", "internal", "life-buoy", "/tickets", "all", 5, false, false},
	}
	for _, m := range modules {
		db.Exec(`
			INSERT INTO modules (name, type, icon, url, is_enabled, is_system, position, access_role)
			SELECT $1, $2, $3, $4, $6, $7, $5, $8
			WHERE NOT EXISTS (SELECT 1 FROM modules WHERE name = $1)
		`, m.name, m.typ, m.icon, m.url, m.position, m.enabled, m.system, m.role)
	}

	// Migrate existing deployments: ensure correct positions, icons, system flags
	// AND correct access_role for the admin-only modules so older deployments
	// stop showing the Admin/Infrastructure tabs to regular users.
	db.Exec(`UPDATE modules SET position = 1, is_enabled = TRUE, is_system = TRUE, access_role = 'all' WHERE name = 'Servers'`)
	db.Exec(`INSERT INTO modules (name, type, icon, url, is_enabled, is_system, position, access_role) SELECT 'Admin', 'internal', 'shield-check', '/admin', TRUE, TRUE, 2, 'admin' WHERE NOT EXISTS (SELECT 1 FROM modules WHERE name = 'Admin')`)
	db.Exec(`UPDATE modules SET position = 2, is_enabled = TRUE, is_system = TRUE, access_role = 'admin' WHERE name = 'Admin'`)
	db.Exec(`UPDATE modules SET position = 3, is_enabled = TRUE, is_system = TRUE, icon = 'cpu', access_role = 'admin' WHERE name = 'Infrastructure'`)
	db.Exec(`UPDATE modules SET position = 4, is_system = FALSE, icon = 'folder-open', access_role = 'admin' WHERE name = 'Library'`)
	// Gateway was retired as a standalone module — its UI moved into the
	// Infrastructure module's Routes tab. Drop the row from existing installs.
	db.Exec(`DELETE FROM modules WHERE name = 'Gateway'`)
	// 'Tickets' was previously deleted as a system module — Phase 2 reintroduces
	// it as a non-system, opt-in module. Drop only the legacy system row.
	db.Exec(`DELETE FROM modules WHERE name IN ('Console', 'Modpacks', 'Files') AND is_system = TRUE`)
	db.Exec(`DELETE FROM modules WHERE name = 'Tickets' AND is_system = TRUE`)
	// Migrate existing Tickets row if present from prior phase: ensure it
	// keeps the correct icon + non-system flag + position.
	db.Exec(`UPDATE modules SET icon = 'life-buoy', url = '/tickets', is_system = FALSE, access_role = 'all', position = 5 WHERE name = 'Tickets'`)

	// Migrate route limits: old semantics had 0 = unlimited, new semantics use -1 = unlimited
	db.Exec(`UPDATE gateway_route_limits SET max_routes = -1 WHERE max_routes = 0`)
	// Migrate port:80 → port:443
	db.Exec(`UPDATE gateway_route_limits SET scope = 'port:443' WHERE scope = 'port:80'`)
}

// applyPhase18Schema (Phase 18 — Telemetry Heartbeat) seeds the toggle for
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

// applyPhase17Schema (Phase 17 — First-Run Setup Wizard) seeds the two
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

// applyAdminResetEnvIfRequested (Phase 17 — Admin Recovery)
//
// If the operator sets DYLARIS_RESET_ADMINS=<any-unique-nonce>, this:
//   1. Demotes every admin (is_admin=true) to role=user, wipes their TOTP
//   2. Writes audit_events_identity rows (event_type=admin.demoted_via_env)
//      so the demotion is tracked
//   3. Persists the nonce in settings.last_admin_reset_nonce so a stale
//      env var on the NEXT boot is detected as a no-op + warning, instead
//      of re-nuking a freshly-created admin
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

func createGatewayTables(db *sql.DB) error {
	tables := []string{
		// Links table (managed by Hub via GORM, but created here for first-run).
		// Constraint name `uni_gateway_links_token` matches GORM's
		// uniqueIndex auto-naming so Hub's AutoMigrate doesn't try to
		// rename it on every startup.
		`CREATE TABLE IF NOT EXISTS gateway_links (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			token TEXT NOT NULL,
			enabled BOOLEAN DEFAULT TRUE,
			is_system BOOLEAN DEFAULT FALSE,
			node_id TEXT DEFAULT NULL,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMPTZ,
			CONSTRAINT uni_gateway_links_token UNIQUE (token)
		)`,
		// Routes table (managed by Hub via GORM). Same naming convention.
		`CREATE TABLE IF NOT EXISTS gateway_routes (
			id SERIAL PRIMARY KEY,
			domain TEXT NOT NULL,
			target_ip TEXT NOT NULL,
			target_port INTEGER NOT NULL DEFAULT 25565,
			link_id INTEGER REFERENCES gateway_links(id) ON DELETE SET NULL,
			server_id INTEGER REFERENCES servers(id) ON DELETE CASCADE,
			owner_id UUID REFERENCES users(id) ON DELETE CASCADE,
			server_uuid TEXT DEFAULT '',
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMPTZ,
			CONSTRAINT idx_domain_port UNIQUE (domain, target_port)
		)`,
		// Route limits (still managed by Core raw SQL)
		`CREATE TABLE IF NOT EXISTS gateway_route_limits (
			id SERIAL PRIMARY KEY,
			scope TEXT NOT NULL UNIQUE,
			max_routes INTEGER NOT NULL DEFAULT 0
		)`,
		// System logs (managed by Hub via GORM)
		`CREATE TABLE IF NOT EXISTS gateway_system_logs (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			level TEXT NOT NULL DEFAULT 'INFO',
			source TEXT NOT NULL DEFAULT 'GATEWAY',
			message TEXT NOT NULL
		)`,
	}
	for _, q := range tables {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("gateway table creation error: %w", err)
		}
	}

	// Migration: rename old tables and columns for Hub GORM compatibility
	migrations := []string{
		// Rename gateway_logs -> gateway_system_logs (Hub expects "system_logs" with prefix)
		`DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name='gateway_logs')
			AND NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name='gateway_system_logs') THEN
				ALTER TABLE gateway_logs RENAME TO gateway_system_logs;
			END IF;
		END $$`,
		// Drop gateway_gates table (gates are now auto-discovered via Redis)
		`DROP TABLE IF EXISTS gateway_gates`,
		// Migrate node_id from INTEGER to TEXT (Hub uses string NodeID)
		`ALTER TABLE gateway_links DROP CONSTRAINT IF EXISTS gateway_links_node_id_fkey`,
		`DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM information_schema.columns
				WHERE table_name='gateway_links' AND column_name='node_id' AND data_type='integer') THEN
				ALTER TABLE gateway_links ALTER COLUMN node_id TYPE TEXT USING node_id::TEXT;
			END IF;
		END $$`,
		// Add deleted_at columns if missing (GORM soft deletes)
		`ALTER TABLE gateway_links ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ`,
		`ALTER TABLE gateway_routes ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ`,
		// Add server_uuid column if missing
		`ALTER TABLE gateway_routes ADD COLUMN IF NOT EXISTS server_uuid TEXT DEFAULT ''`,
		// Normalise the gateway_links.token unique constraint name to what
		// Hub's GORM AutoMigrate expects (uni_<table>_<column>). Older
		// deployments had the Postgres-auto-named gateway_links_token_key
		// which made Hub's migration fatal on every restart.
		`DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname='gateway_links_token_key' AND conrelid='gateway_links'::regclass)
			AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='uni_gateway_links_token' AND conrelid='gateway_links'::regclass) THEN
				ALTER TABLE gateway_links RENAME CONSTRAINT gateway_links_token_key TO uni_gateway_links_token;
			END IF;
		END $$`,
		// Same idea for gateway_routes domain+target_port composite unique.
		`DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname='gateway_routes_domain_target_port_key' AND conrelid='gateway_routes'::regclass)
			AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='idx_domain_port' AND conrelid='gateway_routes'::regclass) THEN
				ALTER TABLE gateway_routes RENAME CONSTRAINT gateway_routes_domain_target_port_key TO idx_domain_port;
			END IF;
		END $$`,
	}
	for _, q := range migrations {
		db.Exec(q) // Ignore errors for idempotent migrations
	}

	return nil
}

// library_disabled stores paths (relative to the library root) that admins
// have hidden from non-admin users. A path is enabled IFF no row exists for
// it AND no row exists for any of its ancestors. Defaulting to "enabled when
// absent" keeps the table small — only the curated exclusions are persisted.
func createLibraryDisabledTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS library_disabled (
		path TEXT PRIMARY KEY,
		disabled_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
	)`)
	return err
}

// applyWarpSchema creates the Warp peer registry + enrollment API keys.
// warp_api_keys: enrollment credentials with a connection policy.
// warp_peers: one row per enrolled client (pubkey ↔ allocated WG IP).
func applyWarpSchema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS warp_api_keys (
		id           SERIAL PRIMARY KEY,
		name         VARCHAR(128) NOT NULL,
		key_hash     VARCHAR(64)  NOT NULL UNIQUE,
		policy       VARCHAR(16)  NOT NULL DEFAULT 'general',
		max_conns    INTEGER      NOT NULL DEFAULT 1,
		on_new_conn  VARCHAR(16)  NOT NULL DEFAULT 'block',
		fixed_wg_ip  TEXT,
		node_id      TEXT,
		revoked_at   TIMESTAMPTZ,
		created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("warp: create warp_api_keys: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS warp_peers (
		id           SERIAL PRIMARY KEY,
		api_key_id   INTEGER      NOT NULL REFERENCES warp_api_keys(id) ON DELETE CASCADE,
		pubkey       TEXT         NOT NULL UNIQUE,
		wg_ip        TEXT         NOT NULL UNIQUE,
		leader_id    TEXT         NOT NULL DEFAULT 'leader-01',
		created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("warp: create warp_peers: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_warp_peers_key
		ON warp_peers(api_key_id)`); err != nil {
		return fmt.Errorf("warp: create warp_peers index: %w", err)
	}
	return nil
}

func createServerStatsTable(db *sql.DB) error {
	query := `CREATE TABLE IF NOT EXISTS server_stats (
		time TIMESTAMPTZ NOT NULL,
		server_uuid TEXT NOT NULL,
		cpu DOUBLE PRECISION,
		cpu_limit DOUBLE PRECISION,
		mem_used BIGINT,
		mem_limit BIGINT,
		players INT,
		max_players INT
	)`
	if _, err := db.Exec(query); err != nil {
		return err
	}

	db.Exec(`CREATE INDEX IF NOT EXISTS idx_server_stats_uuid_time ON server_stats (server_uuid, time DESC)`)

	// Attempt TimescaleDB hypertable conversion (non-fatal if TimescaleDB not installed)
	_, err := db.Exec(`SELECT create_hypertable('server_stats', 'time', if_not_exists => TRUE)`)
	if err != nil {
		log.Printf("TimescaleDB hypertable not created (extension may not be installed): %v", err)
	} else {
		log.Println("TimescaleDB hypertable 'server_stats' ready")
		// Attempt retention policy
		_, err = db.Exec(`SELECT add_retention_policy('server_stats', INTERVAL '24 hours', if_not_exists => TRUE)`)
		if err != nil {
			log.Printf("TimescaleDB retention policy not set: %v", err)
		} else {
			log.Println("TimescaleDB 24h retention policy active on server_stats")
		}
	}

	return nil
}
