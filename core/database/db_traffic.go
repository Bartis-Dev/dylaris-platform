package database

import (
	"database/sql"
	"fmt"
)

// applyTrafficSchema creates the per-tenant traffic-usage table. Idempotent.
// One row per (tenant user, billing month). edge_bytes is THE billing number
// (player traffic measured at the regional edge); relay_bytes (beam filebrowser
// relay) and backup_bytes (R2 storage snapshot) are observability + future
// overage. Harmless when feature_byon_enabled is off: nothing writes to it
// because the aggregator only runs in BYON mode.
func applyTrafficSchema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS traffic_usage (
		user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		period       DATE NOT NULL,              -- first day of the billing month (UTC)
		edge_bytes   BIGINT NOT NULL DEFAULT 0,  -- player traffic at the edge (the bill)
		relay_bytes  BIGINT NOT NULL DEFAULT 0,  -- beam relay filebrowser traffic (observability)
		backup_bytes BIGINT NOT NULL DEFAULT 0,  -- R2 backup storage snapshot (gauge, not cumulative)
		updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (user_id, period)
	)`); err != nil {
		return fmt.Errorf("traffic: create traffic_usage: %w", err)
	}
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_traffic_usage_period ON traffic_usage(period)`)
	return nil
}
