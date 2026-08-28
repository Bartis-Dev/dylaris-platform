package database

import (
	"database/sql"
	"fmt"
)

// applyUpdatesSeenSchema stores which RELEASE each user has acknowledged in the
// updates view, so the badge follows them across browsers.
//
// It used to be two integers, because a build was identified by the line count
// of an append-only feed. Releases are named now, and a version says the same
// thing in a form a human can also read. The counters are dropped rather than
// left behind: nothing reads them, and an orphaned column is a trap for the
// next person who greps for "updates_seen".
//
// Empty means "acknowledged nothing", so the first release reads as unseen.
func applyUpdatesSeenSchema(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS updates_seen_version TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("updates: add users.updates_seen_version: %w", err)
	}
	for _, col := range []string{"updates_seen_platform", "updates_seen_gateway"} {
		if _, err := db.Exec(`ALTER TABLE users DROP COLUMN IF EXISTS ` + col); err != nil {
			return fmt.Errorf("updates: drop users.%s: %w", col, err)
		}
	}
	return nil
}
