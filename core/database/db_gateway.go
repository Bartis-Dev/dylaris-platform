package database

import (
	"database/sql"
	"fmt"
)

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
			CONSTRAINT idx_route_domain UNIQUE (domain)
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
	}
	for _, q := range migrations {
		db.Exec(q) // Ignore errors for idempotent migrations
	}

	return nil
}
