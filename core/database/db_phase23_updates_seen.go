package database

import (
	"database/sql"
	"fmt"
)

// applyUpdatesSeenSchema adds the per-user acknowledged counters for the
// in-panel update feed (the admin "what's new" bell). Additive + idempotent.
// Both default 0, so every existing user starts with an empty badge and the
// first feed entries read as unseen.
func applyUpdatesSeenSchema(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS updates_seen_platform INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("updates feed: add users.updates_seen_platform: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS updates_seen_gateway INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("updates feed: add users.updates_seen_gateway: %w", err)
	}
	return nil
}
