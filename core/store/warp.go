package store

import (
	"database/sql"
	"errors"
)

func (s *PostgresStore) CreateWarpAPIKey(k WarpAPIKey) (int, error) {
	var id int
	err := s.db.QueryRow(`
		INSERT INTO warp_api_keys (name, key_hash, policy, max_conns, on_new_conn, fixed_wg_ip, node_id)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''))
		RETURNING id`,
		k.Name, k.KeyHash, k.Policy, k.MaxConns, k.OnNewConn, k.FixedWGIP, k.NodeID,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) GetWarpAPIKeyByHash(hash string) (*WarpAPIKey, error) {
	var k WarpAPIKey
	var fixedIP, nodeID sql.NullString
	err := s.db.QueryRow(`
		SELECT id, name, key_hash, policy, max_conns, on_new_conn,
		       COALESCE(fixed_wg_ip,''), COALESCE(node_id,''), revoked_at, created_at
		FROM warp_api_keys WHERE key_hash = $1`, hash).
		Scan(&k.ID, &k.Name, &k.KeyHash, &k.Policy, &k.MaxConns, &k.OnNewConn,
			&fixedIP, &nodeID, &k.RevokedAt, &k.CreatedAt)
	if err != nil {
		return nil, err
	}
	k.FixedWGIP, k.NodeID = fixedIP.String, nodeID.String
	return &k, nil
}

func (s *PostgresStore) InsertWarpPeer(p WarpPeer) (int, error) {
	var id int
	err := s.db.QueryRow(`
		INSERT INTO warp_peers (api_key_id, pubkey, wg_ip, leader_id)
		VALUES ($1,$2,$3,$4) RETURNING id`,
		p.APIKeyID, p.Pubkey, p.WGIP, p.LeaderID).Scan(&id)
	return id, err
}

func (s *PostgresStore) GetWarpPeerByPubkey(pubkey string) (*WarpPeer, error) {
	var p WarpPeer
	err := s.db.QueryRow(`
		SELECT id, api_key_id, pubkey, wg_ip, leader_id, created_at
		FROM warp_peers WHERE pubkey = $1`, pubkey).
		Scan(&p.ID, &p.APIKeyID, &p.Pubkey, &p.WGIP, &p.LeaderID, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *PostgresStore) ListWarpPeersByKey(apiKeyID int) ([]WarpPeer, error) {
	rows, err := s.db.Query(`
		SELECT id, api_key_id, pubkey, wg_ip, leader_id, created_at
		FROM warp_peers WHERE api_key_id = $1 ORDER BY id`, apiKeyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWarpPeers(rows)
}

func (s *PostgresStore) ListAllWarpPeers() ([]WarpPeer, error) {
	rows, err := s.db.Query(`
		SELECT id, api_key_id, pubkey, wg_ip, leader_id, created_at
		FROM warp_peers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWarpPeers(rows)
}

func (s *PostgresStore) DeleteWarpPeerByPubkey(pubkey string) error {
	_, err := s.db.Exec(`DELETE FROM warp_peers WHERE pubkey = $1`, pubkey)
	return err
}

func scanWarpPeers(rows *sql.Rows) ([]WarpPeer, error) {
	var out []WarpPeer
	for rows.Next() {
		var p WarpPeer
		if err := rows.Scan(&p.ID, &p.APIKeyID, &p.Pubkey, &p.WGIP, &p.LeaderID, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrWarpLimitReached is returned by EnrollPeerTx when the key's connection
// limit is hit under a "block" policy.
var ErrWarpLimitReached = errors.New("warp connection limit reached")

// warpEnrollLock serializes all warp enrollments cluster-wide via a Postgres
// transaction advisory lock, so concurrent enrolls (across N Cores) can never
// exceed a key's max connections or collide on an allocated IP. Enroll is rare,
// so global serialization is fine.
const warpEnrollLock int64 = 0x77617270 // "warp"

// EnrollPeerTx atomically enforces the key's connection limit, allocates a WG IP
// (via allocIP, given the set of currently-taken IPs), and inserts the peer —
// all under one transaction + advisory lock. For "kill_old" at the limit it
// evicts the oldest peer for the key and returns its pubkey in `evicted`.
// Returns ErrWarpLimitReached when the limit is hit under any non-"kill_old" policy.
func (s *PostgresStore) EnrollPeerTx(keyID, limit int, onNewConn, pubkey, fixedIP, leaderID string, allocIP func(taken map[string]bool) (string, error)) (wgIP string, evicted string, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock($1)`, warpEnrollLock); err != nil {
		return "", "", err
	}

	var count int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM warp_peers WHERE api_key_id = $1`, keyID).Scan(&count); err != nil {
		return "", "", err
	}
	if count >= limit {
		if onNewConn != "kill_old" {
			return "", "", ErrWarpLimitReached
		}
		if err = tx.QueryRow(
			`DELETE FROM warp_peers
			 WHERE id = (SELECT id FROM warp_peers WHERE api_key_id = $1 ORDER BY id LIMIT 1)
			 RETURNING pubkey`, keyID).Scan(&evicted); err != nil {
			return "", "", err
		}
	}

	wgIP = fixedIP
	if wgIP == "" {
		taken := map[string]bool{}
		rows, qerr := tx.Query(`SELECT wg_ip FROM warp_peers`)
		if qerr != nil {
			return "", "", qerr
		}
		for rows.Next() {
			var ip string
			if serr := rows.Scan(&ip); serr != nil {
				rows.Close()
				return "", "", serr
			}
			taken[ip] = true
		}
		rows.Close()
		if rerr := rows.Err(); rerr != nil {
			return "", "", rerr
		}
		if wgIP, err = allocIP(taken); err != nil {
			return "", "", err
		}
	}

	if _, err = tx.Exec(
		`INSERT INTO warp_peers (api_key_id, pubkey, wg_ip, leader_id) VALUES ($1,$2,$3,$4)`,
		keyID, pubkey, wgIP, leaderID); err != nil {
		return "", "", err
	}
	if err = tx.Commit(); err != nil {
		return "", "", err
	}
	return wgIP, evicted, nil
}
