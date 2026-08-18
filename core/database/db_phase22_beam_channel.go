package database

import (
	"database/sql"
	"fmt"
)

// applyBeamChannelSchema is a TOMBSTONE for the Beam prerelease ("dev") update
// channel.
//
// The feature was: a per-user users.beam_update_channel preference plus a
// beam.dev_channel_access policy ('disabled' | 'admins-only' | 'all-users')
// deciding who could opt into it. It was removed - there is one Beam release
// stream, so a second channel only ever added a way for a user's client to
// diverge from what the platform actually ships against.
//
// The retired settings rows are deleted because nothing reads them and leaving
// them invites someone to "fix" them later. users.beam_update_channel is
// deliberately left alone where it already exists: dropping a column is the one
// irreversible half of this change, a Core rollback would then fail on a missing
// column, and an unread column costs nothing. Fresh installs simply never get
// it.
func applyBeamChannelSchema(db *sql.DB) error {
	if _, err := db.Exec(`DELETE FROM settings WHERE key IN ('beam.dev_channel_access', 'beam.min_version', 'beam.min_version_mode')`); err != nil {
		return fmt.Errorf("beam channel: drop retired settings: %w", err)
	}
	return nil
}
