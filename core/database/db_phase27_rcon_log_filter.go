package database

import (
	"database/sql"
	"fmt"
)

// applyRconLogFilterSchema adds the per-server "hide RCON connection noise"
// console toggle.
//
// The panel polls RCON continuously (the online-player list every 10s, plus
// status reads), and Minecraft logs a thread start AND a shutdown for every one
// of those connections. On a server with RCON on, those two lines at the polling
// rate crowd the server's own output out of the 1000-entry log stream.
//
// Defaults to FALSE: the lines are real server output, and silently hiding
// output nobody asked to hide is worse than noise. The operator opts in per
// server under Players -> RCON.
//
// The log-shipper reads the live value from Redis
// (dylaris:server:<uuid>:log_filter_rcon), not from an env var, so the toggle
// takes effect on a running server without recreating its container. This column
// is the durable source that Redis key is published from.
func applyRconLogFilterSchema(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE servers ADD COLUMN IF NOT EXISTS rcon_log_filter BOOLEAN NOT NULL DEFAULT FALSE`); err != nil {
		return fmt.Errorf("rcon log filter: add servers.rcon_log_filter: %w", err)
	}
	return nil
}
