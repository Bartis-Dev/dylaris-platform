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

	// product is which PRODUCT moved the bytes: "byon" for a server on a machine
	// the tenant owns, "route" for a protected address that reaches their own
	// server. It is derived in the aggregator from the counter's SUBJECT
	// (an "owner:" subject is route-only, a bare server UUID is a BYON node), so
	// no producer had to learn a new field and no wire format changed.
	//
	// A BREAKDOWN dimension only. The allowance is granted per unit HELD and
	// pooled across products, so limit resolution folds product away before it
	// compares anything - splitting the pool per product would hand a tenant the
	// same free allowance once per product they own.
	//
	// Empty is the honest value for rows written before this existed and for a
	// subject whose product cannot be told: those bytes are real, they are in
	// the billing total, and dropping them would make the split stop adding up
	// to the number the tenant is charged on. Same reason "unknown" exists for
	// the region.
	if _, err := db.Exec(`ALTER TABLE traffic_usage_region
		ADD COLUMN IF NOT EXISTS product TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("traffic: add traffic_usage_region.product: %w", err)
	}
	// The primary key has to grow with it, or the first product to write a
	// (region, kind) cell owns it and the second is folded into that row by the
	// upsert - which is not a lost split but a WRONG one, silently attributing
	// one product's bytes to the other.
	if err := widenTrafficRegionKey(db); err != nil {
		return err
	}

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

	// Fold the retired "global" scope into the tenant default.
	//
	// It answered nothing "user_default" could not - every byte counted here
	// belongs to a tenant - and two scopes doing one job is a screen where the
	// value an operator typed stops applying the day somebody fills in the other.
	// A global row is only carried over where the default has NO row, because a
	// default row already answered and was already winning.
	if _, err := db.Exec(`
		INSERT INTO traffic_limits (scope, region, kind, included_gb, max_purchase_gb)
		SELECT 'user_default', region, kind, included_gb, max_purchase_gb
		FROM traffic_limits WHERE scope = 'global'
		ON CONFLICT (scope, region, kind) DO NOTHING`); err != nil {
		return fmt.Errorf("traffic: fold global scope: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM traffic_limits WHERE scope = 'global'`); err != nil {
		return fmt.Errorf("traffic: drop global scope: %w", err)
	}

	// Move every NON-regional kind onto the single '*' region.
	//
	// File transfers are carried by relays that all sit in eu-central, so a
	// per-region cap for them was a row per region answering one question. The
	// delete keeps the lowest id per (scope, kind), which is arbitrary only where
	// an operator had configured the same kind in two regions - and there the
	// alternative is inventing a merge rule for numbers that meant "per region".
	if _, err := db.Exec(`
		DELETE FROM traffic_limits t
		WHERE t.kind <> 'edge' AND t.region <> '*'
		  AND EXISTS (
			SELECT 1 FROM traffic_limits k
			WHERE k.scope = t.scope AND k.kind = t.kind
			  AND (k.region = '*' OR k.id < t.id))`); err != nil {
		return fmt.Errorf("traffic: dedupe non-regional limits: %w", err)
	}
	if _, err := db.Exec(`UPDATE traffic_limits SET region = '*' WHERE kind <> 'edge' AND region <> '*'`); err != nil {
		return fmt.Errorf("traffic: move non-regional limits: %w", err)
	}
	return nil
}

// widenTrafficRegionKey adds product to traffic_usage_region's primary key.
//
// Idempotent by asking the catalog rather than by catching an error: an
// ADD PRIMARY KEY that fails because the key already exists and one that fails
// because the table is in some other state look the same from here, and this
// runs on every boot.
//
// The DROP and the ADD are one transaction. Between them the table has no
// primary key at all, and the aggregator's upsert has no conflict target to
// name - a tick landing in that window would fail rather than double-count, but
// only because it is a plain INSERT ... ON CONFLICT that errors; the window is
// closed here so it cannot be reasoned about wrongly later.
func widenTrafficRegionKey(db *sql.DB) error {
	var keyed bool
	err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.key_column_usage k
			JOIN information_schema.table_constraints c
			  ON c.constraint_name = k.constraint_name
			 AND c.constraint_schema = k.constraint_schema
			WHERE c.table_name = 'traffic_usage_region'
			  AND c.constraint_type = 'PRIMARY KEY'
			  AND k.column_name = 'product'
		)`).Scan(&keyed)
	if err != nil {
		return fmt.Errorf("traffic: inspect traffic_usage_region key: %w", err)
	}
	if keyed {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("traffic: widen traffic_usage_region key: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE traffic_usage_region DROP CONSTRAINT IF EXISTS traffic_usage_region_pkey`); err != nil {
		return fmt.Errorf("traffic: drop traffic_usage_region key: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE traffic_usage_region ADD PRIMARY KEY (user_id, period, region, kind, product)`); err != nil {
		return fmt.Errorf("traffic: widen traffic_usage_region key: %w", err)
	}
	return tx.Commit()
}
