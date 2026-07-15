package database

import (
	"database/sql"
	"fmt"
)

// applyAuthzGrantsSchema adds the F6 partial unique index for account-wide
// grants (phase 3). ADDITIVE ONLY. The reworked server_invites table lets
// server_id be NULL for an account-wide grant ("all the owner's servers + owner
// tools"), but the existing UNIQUE(server_id, user_id) does NOT prevent
// duplicate account-wide rows because Postgres treats NULL server_id as
// distinct. This partial unique index enforces one account-wide grant per
// (owner, friend). Idempotent via IF NOT EXISTS.
func applyAuthzGrantsSchema(db *sql.DB) error {
	if _, err := db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_server_invites_account_grant ON server_invites (owner_user_id, user_id) WHERE server_id IS NULL`); err != nil {
		return fmt.Errorf("authz grants: create account-grant unique index: %w", err)
	}
	return nil
}
