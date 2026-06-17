package database

import (
	"database/sql"
	"fmt"
)

// applyBYONSchema creates the BYON (bring-your-own-node) tables. Idempotent.
// Only used when feature_byon_enabled; the tables are harmless when the feature
// is off (no rows, never read).
func applyBYONSchema(db *sql.DB) error {
	// node_enroll_tokens: per-user tokens a tenant uses to enroll their own node.
	// The plaintext token is only ever returned once at mint time; we store a
	// hash. Reusable until expires_at (NULL = no expiry); revoked by deleting the
	// row. The plan's max_nodes is enforced separately at enroll time.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS node_enroll_tokens (
		id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		token_hash  TEXT NOT NULL UNIQUE,
		label       TEXT NOT NULL DEFAULT '',
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		expires_at  TIMESTAMPTZ
	)`); err != nil {
		return fmt.Errorf("byon: create node_enroll_tokens: %w", err)
	}
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_node_enroll_tokens_user ON node_enroll_tokens(user_id)`)
	return nil
}
