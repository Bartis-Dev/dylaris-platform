package database

import (
	"database/sql"
	"fmt"
)

// applyBeamChannelSchema adds the per-user Beam update-channel preference and
// seeds the admin-set dev-channel access policy. Additive + idempotent.
//
//   - users.beam_update_channel: 'stable' (default) or 'dev'. The Beam desktop
//     app checks the matching release manifest for updates. 'dev' is only ever
//     honored when the beam.dev_channel_access policy admits the user.
//   - beam.dev_channel_access setting: 'disabled' (default), 'admins-only', or
//     'all-users'. Gates who may opt into the dev (prerelease) channel.
func applyBeamChannelSchema(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS beam_update_channel VARCHAR(16) NOT NULL DEFAULT 'stable'`); err != nil {
		return fmt.Errorf("beam channel: add users.beam_update_channel: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES ('beam.dev_channel_access', 'disabled') ON CONFLICT (key) DO NOTHING`); err != nil {
		return fmt.Errorf("beam channel: seed dev_channel_access: %w", err)
	}
	return nil
}
