package database

import (
	"database/sql"
	"fmt"
)

// applyWarpSchema creates the Warp peer registry + enrollment API keys.
// warp_api_keys: enrollment credentials with a connection policy.
// warp_peers: one row per enrolled client (pubkey ↔ allocated WG IP).
func applyWarpSchema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS warp_api_keys (
		id           SERIAL PRIMARY KEY,
		name         VARCHAR(128) NOT NULL,
		key_hash     VARCHAR(64)  NOT NULL UNIQUE,
		policy       VARCHAR(16)  NOT NULL DEFAULT 'general',
		max_conns    INTEGER      NOT NULL DEFAULT 1,
		on_new_conn  VARCHAR(16)  NOT NULL DEFAULT 'block',
		fixed_wg_ip  TEXT,
		node_id      TEXT,
		revoked_at   TIMESTAMPTZ,
		created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("warp: create warp_api_keys: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS warp_peers (
		id           SERIAL PRIMARY KEY,
		api_key_id   INTEGER      NOT NULL REFERENCES warp_api_keys(id) ON DELETE CASCADE,
		pubkey       TEXT         NOT NULL UNIQUE,
		wg_ip        TEXT         NOT NULL UNIQUE,
		leader_id    TEXT         NOT NULL DEFAULT 'leader-01',
		created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("warp: create warp_peers: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_warp_peers_key
		ON warp_peers(api_key_id)`); err != nil {
		return fmt.Errorf("warp: create warp_peers index: %w", err)
	}
	return nil
}
