package database

import (
	"database/sql"
	"dylaris-core/config"
	"fmt"
	"log"

	_ "github.com/lib/pq" // Required: Postgres Driver
	"golang.org/x/crypto/bcrypt"
)

func InitDB(cfg config.Config) (*sql.DB, error) {
	// Postgres Connection String (DSN)
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("DB Open Error: %w", err)
	}

	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("DB Ping Error: %w", err)
	}

	log.Println("Postgres DB connection established.")

	// Dev only: wipe everything before init when DYLARIS_CLEAR_DEV is true.
	if cfg.ClearDev {
		if err := WipeAll(db); err != nil {
			return nil, fmt.Errorf("DEV CLEAR failed: %w", err)
		}
	}

	// Create tables (schema)
	if err := createUsersTable(db); err != nil {
		return nil, err
	}
	if err := createModulesTable(db); err != nil {
		return nil, err
	}
	if err := createNodesTable(db); err != nil {
		return nil, err
	}
	if err := createServersTable(db); err != nil {
		return nil, err
	}
	if err := createSettingsTable(db); err != nil {
		return nil, err
	}
	if err := createServerInvitesTable(db); err != nil {
		return nil, err
	}
	if err := createServerStatsTable(db); err != nil {
		return nil, err
	}
	if err := createGatewayTables(db); err != nil {
		return nil, err
	}
	if err := createLibraryDisabledTable(db); err != nil {
		return nil, err
	}

	if err := migrateSchema(db); err != nil {
		return nil, err
	}

	seedSystemModules(db)
	seedDefaultAdmin(db)

	return db, nil
}

func createUsersTable(db *sql.DB) error {
	query := `CREATE TABLE IF NOT EXISTS users (
		id SERIAL PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		password TEXT NOT NULL,
		minecraft_username TEXT,
		email TEXT,
		is_admin BOOLEAN DEFAULT FALSE,
		is_2fa_enabled BOOLEAN DEFAULT FALSE,
		permissions TEXT,
		public_id TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`
	_, err := db.Exec(query)
	return err
}

func createModulesTable(db *sql.DB) error {
	query := `CREATE TABLE IF NOT EXISTS modules (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL,
		type TEXT NOT NULL,
		icon TEXT NOT NULL,
		url TEXT,
		is_enabled BOOLEAN DEFAULT TRUE,
		is_system BOOLEAN DEFAULT FALSE,
		position INTEGER DEFAULT 99
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
		owner_id INTEGER NOT NULL REFERENCES users(id),
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
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		permissions JSONB DEFAULT '{}',
		invited_by INTEGER NOT NULL REFERENCES users(id),
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
	}
	for _, c := range cols {
		query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s", c.table, c.col, c.def)
		db.Exec(query)
	}

	// Unique constraints (idempotent)
	db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_name_unique ON nodes (name)`)

	return nil
}

// seedSystemModules inserts system modules that don't exist yet and migrates existing ones
func seedSystemModules(db *sql.DB) {
	modules := []struct {
		name, typ, icon, url string
		position             int
		enabled, system      bool
	}{
		{"Servers", "internal", "server", "/servers", 1, true, true},
		{"Admin", "internal", "shield-check", "/admin", 2, true, true},
		{"Infrastructure", "internal", "cpu", "/infrastructure", 3, true, true},
		{"Gateway", "internal", "globe", "/gateway", 4, true, false},
		{"Library", "internal", "folder-open", "/library", 5, false, false},
	}
	for _, m := range modules {
		db.Exec(`
			INSERT INTO modules (name, type, icon, url, is_enabled, is_system, position)
			SELECT $1, $2, $3, $4, $6, $7, $5
			WHERE NOT EXISTS (SELECT 1 FROM modules WHERE name = $1)
		`, m.name, m.typ, m.icon, m.url, m.position, m.enabled, m.system)
	}

	// Migrate existing deployments: ensure correct positions, icons, and system flags
	db.Exec(`UPDATE modules SET position = 1, is_enabled = TRUE, is_system = TRUE WHERE name = 'Servers'`)
	db.Exec(`INSERT INTO modules (name, type, icon, url, is_enabled, is_system, position) SELECT 'Admin', 'internal', 'shield-check', '/admin', TRUE, TRUE, 2 WHERE NOT EXISTS (SELECT 1 FROM modules WHERE name = 'Admin')`)
	db.Exec(`UPDATE modules SET position = 3, is_system = TRUE, icon = 'cpu' WHERE name = 'Infrastructure'`)
	db.Exec(`UPDATE modules SET position = 4, is_system = FALSE, is_enabled = TRUE WHERE name = 'Gateway'`)
	db.Exec(`UPDATE modules SET position = 5, is_system = FALSE, icon = 'folder-open' WHERE name = 'Library'`)
	db.Exec(`DELETE FROM modules WHERE name IN ('Console', 'Modpacks', 'Files', 'Tickets') AND is_system = TRUE`)

	// Migrate route limits: old semantics had 0 = unlimited, new semantics use -1 = unlimited
	db.Exec(`UPDATE gateway_route_limits SET max_routes = -1 WHERE max_routes = 0`)
	// Migrate port:80 → port:443
	db.Exec(`UPDATE gateway_route_limits SET scope = 'port:443' WHERE scope = 'port:80'`)
}

func seedDefaultAdmin(db *sql.DB) {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if count > 0 {
		return
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte("dylaris"), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Failed to hash default admin password: %v", err)
		return
	}
	_, err = db.Exec(
		`INSERT INTO users (username, password, is_admin, public_id) VALUES ($1, $2, TRUE, 'admin-default')`,
		"dylaris", string(hashed),
	)
	if err != nil {
		log.Printf("Failed to seed default admin: %v", err)
		return
	}
	log.Println("Default admin user created (dylaris/dylaris) — change the password after first login!")
}

func createGatewayTables(db *sql.DB) error {
	tables := []string{
		// Links table (managed by Hub via GORM, but created here for first-run)
		`CREATE TABLE IF NOT EXISTS gateway_links (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			token TEXT NOT NULL UNIQUE,
			enabled BOOLEAN DEFAULT TRUE,
			is_system BOOLEAN DEFAULT FALSE,
			node_id TEXT DEFAULT NULL,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMPTZ
		)`,
		// Routes table (managed by Hub via GORM)
		`CREATE TABLE IF NOT EXISTS gateway_routes (
			id SERIAL PRIMARY KEY,
			domain TEXT NOT NULL,
			target_ip TEXT NOT NULL,
			target_port INTEGER NOT NULL DEFAULT 25565,
			link_id INTEGER REFERENCES gateway_links(id) ON DELETE SET NULL,
			server_id INTEGER REFERENCES servers(id) ON DELETE CASCADE,
			owner_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
			server_uuid TEXT DEFAULT '',
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMPTZ,
			UNIQUE(domain, target_port)
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

// WipeAll drops the entire public schema and recreates it.
// Used by DYLARIS_CLEAR_DEV=true on startup to reset all tables (Core + Hub's
// gateway_* tables) for clean dev iterations. Hub will re-seed on its next
// start because its AutoMigrate sees empty tables.
//
// ⚠ DESTRUCTIVE: removes ALL tables, sequences, types in the 'public' schema.
// Never enable in production.
func WipeAll(db *sql.DB) error {
	log.Println("⚠ DEV CLEAR ACTIVE — dropping public schema (all tables + sequences)")
	if _, err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		return fmt.Errorf("drop schema: %w", err)
	}
	// Re-grant default privileges so the configured user can keep working.
	if _, err := db.Exec(`GRANT ALL ON SCHEMA public TO public;`); err != nil {
		return fmt.Errorf("grant schema: %w", err)
	}
	log.Println("⚠ DEV CLEAR — public schema wiped, fresh init follows")
	return nil
}
