package database

import (
	"database/sql"
	"fmt"
)

// applyBillingSchema creates the per-tenant billing/lifecycle table. Idempotent.
// Payment-provider-agnostic by design: status is driven manually today (admin or,
// later, a Stripe/other webhook) and the lifecycle worker progresses it.
//
// IMPORTANT: nothing here ever deletes a user or their DB rows. On non-payment we
// stop services and, after the retention windows, remove the node connection and
// the R2 backups only. The account + server metadata stay.
func applyBillingSchema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_billing (
		user_id        UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		status         TEXT NOT NULL DEFAULT 'active',  -- active | past_due | suspended
		grace_until    TIMESTAMPTZ,                     -- when past_due flips to suspended
		suspended_at   TIMESTAMPTZ,                     -- anchor for the retention windows
		-- per-user retention OVERRIDES (NULL = use the platform default). Format
		-- is a number + unit: d (days), w (weeks), m (months). e.g. '3d','2w','3m'.
		grace_period   TEXT,
		r2_retention   TEXT,
		node_retention TEXT,
		updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("billing: create user_billing: %w", err)
	}
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_user_billing_status ON user_billing(status)`)
	return nil
}
