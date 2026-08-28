package database

import (
	"database/sql"
	"fmt"
)

// createGatewayTables owns only the ONE gateway table Core actually uses.
//
// Core used to also create gateway_links, gateway_routes and gateway_system_logs
// "for first-run", from the era when Core and the Hub shared one database. Core
// never read or wrote a single row of them - everything crossing the Core/Hub
// boundary goes through Redis (the dylaris:hub:queue work queue and the route:*
// keys), never through SQL. Creating them here only meant Core's DDL and the
// Hub's GORM model raced to define the same tables, which is exactly what made
// a shared database unusable: Core declared gateway_routes.owner_id as UUID
// while the Hub modelled an integer, so the Hub's AutoMigrate tried to retype
// the column on every boot. The Hub now creates its own tables from its own
// models, so the two can share a database without colliding.
//
// Existing databases keep those three tables; they are inert and can be dropped
// by hand. gateway_route_limits below is Core's own and stays.
func createGatewayTables(db *sql.DB) error {
	tables := []string{
		// Route limits (managed by Core raw SQL; the Hub has no such table)
		// max_routes is NULLABLE on purpose: NULL is "no cap", 0 is a real
		// "none". It was NOT NULL DEFAULT 0 while 0 meant "disabled", which made
		// this the one limit in the platform where zero did not mean unlimited.
		// See models.GatewayRouteLimit.
		`CREATE TABLE IF NOT EXISTS gateway_route_limits (
			id SERIAL PRIMARY KEY,
			scope TEXT NOT NULL UNIQUE,
			max_routes INTEGER
		)`,
	}
	for _, q := range tables {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("gateway table creation error: %w", err)
		}
	}

	// Legacy shim, only reachable on a database where an OLD Core created the
	// Hub's tables: reshape them into what the Hub's AutoMigrate expects so it
	// does not fatal. On a fresh database the tables do not exist and every
	// statement here is a no-op (errors are ignored on purpose). Deletable once
	// no deployment shares a database with a pre-2026-07 Core.
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
