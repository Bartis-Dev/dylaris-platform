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

	// Per-region, per-KIND split of the same bytes. traffic_usage stays THE
	// billing total: this table is a breakdown of it, not a second source, so a
	// row that fails to write can never make the total wrong.
	//
	// It exists because a TB does not cost the same everywhere - Singapore is
	// 7.4x Nuremberg at the same provider - and one flat allowance therefore
	// prices one region at a loss or the rest at a discount.
	//
	// kind is a COLUMN rather than one column per component, because the set is
	// open: today "edge" (player traffic, region = the edge that served it) and
	// "relay" (beam file transfers, region = the relay). "warp" is deliberately
	// absent - the overlay carries the control plane, not the payload, and it is
	// measurably idle - but it costs nothing to accept later, which is the whole
	// point of the column. A per-component column set would have needed a
	// migration for that; a row value needs none.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS traffic_usage_region (
		user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		period     DATE NOT NULL,              -- first day of the billing month (UTC)
		region     TEXT NOT NULL,              -- e.g. eu-central; "unknown" if a producer did not say
		kind       TEXT NOT NULL,              -- edge | relay | (later) warp
		bytes      BIGINT NOT NULL DEFAULT 0,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (user_id, period, region, kind)
	)`); err != nil {
		return fmt.Errorf("traffic: create traffic_usage_region: %w", err)
	}
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_traffic_usage_region_period ON traffic_usage_region(period)`)
	return nil
}
