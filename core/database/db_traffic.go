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

	// What a tenant may use, and may buy, per (region, kind).
	//
	// Same three-scope shape as gateway_route_limits, deliberately: "user:<id>",
	// then "user_default", then "global". A scope with NO ROW says nothing and
	// the next one is asked; a scope WITH a row has answered, including with
	// NULL. That is why both numbers live in ONE row - a row is one complete
	// answer, so a user override cannot set half a policy and silently inherit
	// the other half from a scope the operator was not looking at.
	//
	// Both columns are NULLABLE and follow the platform-wide convention
	// (CLAUDE.md, "Limits"): NULL is no limit at all, 0 is none - they may hold
	// zero of this - and n is the cap. For max_purchase_gb the difference is the
	// whole feature: 0 means a region where extra traffic cannot be bought at
	// any price, which is the honest answer where a TB costs us more than the
	// subscription (Singapore is 7.4x Nuremberg), and NULL would mean the
	// opposite.
	//
	// GB, not TB: the enforcement already works in GB (billing.CeilingGB), and
	// a 500 GB allowance is not expressible in whole TB.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS traffic_limits (
		id              SERIAL PRIMARY KEY,
		scope           TEXT NOT NULL,   -- user:<id> | user_default | global
		region          TEXT NOT NULL,   -- e.g. eu-central
		kind            TEXT NOT NULL,   -- edge | relay | warp
		included_gb     BIGINT,          -- NULL = no cap, 0 = none, n = the cap
		max_purchase_gb BIGINT,          -- NULL = unlimited, 0 = cannot buy any, n = the cap
		UNIQUE (scope, region, kind)
	)`); err != nil {
		return fmt.Errorf("traffic: create traffic_limits: %w", err)
	}
	return nil
}
