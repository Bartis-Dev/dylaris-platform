package store

import "database/sql"

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
