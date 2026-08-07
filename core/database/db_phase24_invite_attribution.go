package database

import (
	"database/sql"
	"fmt"
)

// applyInviteAttributionNullable lets an access grant outlive the account that
// issued it.
//
// server_invites has three foreign keys into users: server_id and user_id were
// declared ON DELETE CASCADE, invited_by was declared with no ON DELETE clause
// at all - which Postgres reads as NO ACTION. The column is also NOT NULL, so
// SET NULL was not even available to it.
//
// The effect: an admin who ever granted someone access to a server they do not
// themselves own could never be deleted. CreateInvite writes the ACTING user
// into invited_by and derives owner_user_id from the server, so the two really
// do differ, and the cascade on owner_user_id does not cover it. DeleteUser
// answered 409 "still referenced by other records (for example server invites
// they issued)" - accurate, but there is no way to act on it: the operator
// would have to find every grant that account ever issued, across servers they
// may not own.
//
// SET NULL rather than CASCADE, deliberately: CASCADE would revoke a member's
// access because an unrelated admin left, which is a silent permission change.
// The grant stays, the attribution becomes unknown. That is what the other nine
// "who did this" columns in this schema already do (created_by, actor_user_id,
// requested_by, installed_by, added_by are all ON DELETE SET NULL).
//
// The readers were an INNER JOIN on invited_by, so a null would have hidden the
// grant from the members list while the access itself remained - worse than the
// bug. They are LEFT JOINs now (see GetInvite / ListInvitesByServer).
func applyInviteAttributionNullable(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE server_invites ALTER COLUMN invited_by DROP NOT NULL`); err != nil {
		return fmt.Errorf("invite attribution: drop NOT NULL on server_invites.invited_by: %w", err)
	}
	// Drop-then-add is what makes this idempotent: ADD CONSTRAINT has no
	// IF NOT EXISTS, and the name is Postgres's own default for this column so
	// it matches both a fresh install and every existing one.
	if _, err := db.Exec(`ALTER TABLE server_invites DROP CONSTRAINT IF EXISTS server_invites_invited_by_fkey`); err != nil {
		return fmt.Errorf("invite attribution: drop the old server_invites.invited_by constraint: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE server_invites
		ADD CONSTRAINT server_invites_invited_by_fkey
		FOREIGN KEY (invited_by) REFERENCES users(id) ON DELETE SET NULL`); err != nil {
		return fmt.Errorf("invite attribution: re-add server_invites.invited_by as ON DELETE SET NULL: %w", err)
	}
	return nil
}
