package database

import (
	"database/sql"
	"fmt"
)

func createBackupTables(db *sql.DB) error {
	tables := []string{
		// Configured storage backends. Admin-managed.
		`CREATE TABLE IF NOT EXISTS backup_storages (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			provider TEXT NOT NULL,           -- 'local' | 's3'
			config JSONB NOT NULL DEFAULT '{}'::jsonb,
			is_default BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`,
		// Per-server backup job. Multiple jobs allowed per container; each
		// optionally targets a single sub-server (sub_server = NULL means
		// full-container snapshot).
		`CREATE TABLE IF NOT EXISTS backup_jobs (
			id SERIAL PRIMARY KEY,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			sub_server TEXT,
			name TEXT NOT NULL,
			schedule TEXT NOT NULL DEFAULT 'manual',
			include_patterns TEXT[] NOT NULL DEFAULT '{}',
			exclude_patterns TEXT[] NOT NULL DEFAULT '{}',
			retention_count INTEGER NOT NULL DEFAULT 3,
			storage_id INTEGER REFERENCES backup_storages(id) ON DELETE SET NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			last_run_at TIMESTAMPTZ,
			next_run_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_backup_jobs_server ON backup_jobs(server_id)`,
		`CREATE INDEX IF NOT EXISTS idx_backup_jobs_next_run ON backup_jobs(next_run_at) WHERE enabled = TRUE`,
		// One row per actual backup run (snapshot or attempt).
		`CREATE TABLE IF NOT EXISTS backup_runs (
			id SERIAL PRIMARY KEY,
			job_id INTEGER NOT NULL REFERENCES backup_jobs(id) ON DELETE CASCADE,
			started_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMPTZ,
			status TEXT NOT NULL DEFAULT 'running',  -- running | success | failed
			size_bytes BIGINT NOT NULL DEFAULT 0,
			storage_key TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_backup_runs_job ON backup_runs(job_id, started_at DESC)`,
		// One row per restore attempt. We keep history separate from
		// backup_runs because a single archive can be restored many times
		// — collapsing them would mask that.
		`CREATE TABLE IF NOT EXISTS backup_restores (
			id SERIAL PRIMARY KEY,
			run_id INTEGER NOT NULL REFERENCES backup_runs(id) ON DELETE CASCADE,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			requested_by UUID REFERENCES users(id) ON DELETE SET NULL,
			requested_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMPTZ,
			status TEXT NOT NULL DEFAULT 'queued',
			error_message TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_backup_restores_server ON backup_restores(server_id, requested_at DESC)`,
	}
	for _, q := range tables {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("backup table creation error: %w", err)
		}
	}
	return nil
}
