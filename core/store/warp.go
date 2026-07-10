package store

import (
	"database/sql"
	"errors"
)

func (s *PostgresStore) CreateWarpAPIKey(k WarpAPIKey) (int, error) {
	var id int
	err := s.db.QueryRow(`
		INSERT INTO warp_api_keys (name, key_hash, policy, max_conns, on_new_conn, fixed_wg_ip, node_id, region, owner_id)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),NULLIF($9,'')::uuid)
		RETURNING id`,
		k.Name, k.KeyHash, k.Policy, k.MaxConns, k.OnNewConn, k.FixedWGIP, k.NodeID, k.Region, k.OwnerID,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) GetWarpAPIKeyByHash(hash string) (*WarpAPIKey, error) {
	var k WarpAPIKey
	var fixedIP, nodeID, region, ownerID sql.NullString
	err := s.db.QueryRow(`
		SELECT id, name, key_hash, policy, max_conns, on_new_conn,
		       COALESCE(fixed_wg_ip,''), COALESCE(node_id,''), COALESCE(region,''),
		       COALESCE(owner_id::text,''), revoked_at, created_at
		FROM warp_api_keys WHERE key_hash = $1`, hash).
		Scan(&k.ID, &k.Name, &k.KeyHash, &k.Policy, &k.MaxConns, &k.OnNewConn,
			&fixedIP, &nodeID, &region, &ownerID, &k.RevokedAt, &k.CreatedAt)
	if err != nil {
		return nil, err
	}
	k.FixedWGIP, k.NodeID, k.Region, k.OwnerID = fixedIP.String, nodeID.String, region.String, ownerID.String
	return &k, nil
}

// ListWarpAPIKeysByOwner returns the non-revoked warp keys minted for a tenant —
// the link/route-only kits they own. Used by the panel to list "my links".
func (s *PostgresStore) ListWarpAPIKeysByOwner(ownerID string) ([]WarpAPIKey, error) {
	rows, err := s.db.Query(`
		SELECT id, name, key_hash, policy, max_conns, on_new_conn,
		       COALESCE(fixed_wg_ip,''), COALESCE(node_id,''), COALESCE(region,''),
		       COALESCE(owner_id::text,''), revoked_at, created_at
		FROM warp_api_keys
		WHERE owner_id = $1::uuid AND revoked_at IS NULL
		ORDER BY created_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WarpAPIKey
	for rows.Next() {
		var k WarpAPIKey
		var fixedIP, nodeID, region, owner sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyHash, &k.Policy, &k.MaxConns, &k.OnNewConn,
			&fixedIP, &nodeID, &region, &owner, &k.RevokedAt, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.FixedWGIP, k.NodeID, k.Region, k.OwnerID = fixedIP.String, nodeID.String, region.String, owner.String
		out = append(out, k)
	}
	return out, rows.Err()
}

// ListAllLinkKits returns every non-revoked route-only link kit across all
// tenants (their warp key node_id carries the "link-" prefix). Used by the ACL
// reconciler to re-apply route-only link ACL users after a Valkey restart that
// dropped them. Mirrors ListWarpAPIKeysByOwner's scan.
func (s *PostgresStore) ListAllLinkKits() ([]WarpAPIKey, error) {
	rows, err := s.db.Query(`
		SELECT id, name, key_hash, policy, max_conns, on_new_conn,
		       COALESCE(fixed_wg_ip,''), COALESCE(node_id,''), COALESCE(region,''),
		       COALESCE(owner_id::text,''), revoked_at, created_at
		FROM warp_api_keys
		WHERE node_id LIKE 'link-%' AND revoked_at IS NULL
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WarpAPIKey
	for rows.Next() {
		var k WarpAPIKey
		var fixedIP, nodeID, region, owner sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyHash, &k.Policy, &k.MaxConns, &k.OnNewConn,
			&fixedIP, &nodeID, &region, &owner, &k.RevokedAt, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.FixedWGIP, k.NodeID, k.Region, k.OwnerID = fixedIP.String, nodeID.String, region.String, owner.String
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *PostgresStore) InsertWarpPeer(p WarpPeer) (int, error) {
	var id int
	err := s.db.QueryRow(`
		INSERT INTO warp_peers (api_key_id, pubkey, wg_ip, region)
		VALUES ($1,$2,$3,$4) RETURNING id`,
		p.APIKeyID, p.Pubkey, p.WGIP, p.Region).Scan(&id)
	return id, err
}

func (s *PostgresStore) GetWarpPeerByPubkey(pubkey string) (*WarpPeer, error) {
	var p WarpPeer
	err := s.db.QueryRow(`
		SELECT id, api_key_id, pubkey, wg_ip, region, created_at
		FROM warp_peers WHERE pubkey = $1`, pubkey).
		Scan(&p.ID, &p.APIKeyID, &p.Pubkey, &p.WGIP, &p.Region, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *PostgresStore) ListWarpPeersByKey(apiKeyID int) ([]WarpPeer, error) {
	rows, err := s.db.Query(`
		SELECT id, api_key_id, pubkey, wg_ip, region, created_at
		FROM warp_peers WHERE api_key_id = $1 ORDER BY id`, apiKeyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWarpPeers(rows)
}

func (s *PostgresStore) ListAllWarpPeers() ([]WarpPeer, error) {
	rows, err := s.db.Query(`
		SELECT id, api_key_id, pubkey, wg_ip, region, created_at
		FROM warp_peers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWarpPeers(rows)
}

// ListWarpPeersByRegion returns every peer pinned to a region — the full peer set
// a leader of that region must hold (used by the per-leader resync push).
func (s *PostgresStore) ListWarpPeersByRegion(region string) ([]WarpPeer, error) {
	rows, err := s.db.Query(`
		SELECT id, api_key_id, pubkey, wg_ip, region, created_at
		FROM warp_peers WHERE region = $1 ORDER BY id`, region)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWarpPeers(rows)
}

// CountWarpPeersByRegion returns the peer count per region (region -> count), used
// to pick the least-loaded region at enroll.
func (s *PostgresStore) CountWarpPeersByRegion() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT region, COUNT(*) FROM warp_peers GROUP BY region`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var region string
		var n int
		if err := rows.Scan(&region, &n); err != nil {
			return nil, err
		}
		out[region] = n
	}
	return out, rows.Err()
}

func (s *PostgresStore) DeleteWarpPeerByPubkey(pubkey string) error {
	_, err := s.db.Exec(`DELETE FROM warp_peers WHERE pubkey = $1`, pubkey)
	return err
}

func scanWarpPeers(rows *sql.Rows) ([]WarpPeer, error) {
	var out []WarpPeer
	for rows.Next() {
		var p WarpPeer
		if err := rows.Scan(&p.ID, &p.APIKeyID, &p.Pubkey, &p.WGIP, &p.Region, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- Regions ---

func (s *PostgresStore) ListWarpRegions() ([]WarpRegion, error) {
	rows, err := s.db.Query(`SELECT region, subnet, enabled, created_at FROM warp_regions ORDER BY region`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WarpRegion
	for rows.Next() {
		var r WarpRegion
		if err := rows.Scan(&r.Region, &r.Subnet, &r.Enabled, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetWarpRegion(region string) (*WarpRegion, error) {
	var r WarpRegion
	err := s.db.QueryRow(`SELECT region, subnet, enabled, created_at FROM warp_regions WHERE region = $1`, region).
		Scan(&r.Region, &r.Subnet, &r.Enabled, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UpsertWarpRegion inserts or updates a region's subnet + enabled flag.
func (s *PostgresStore) UpsertWarpRegion(region, subnet string, enabled bool) error {
	_, err := s.db.Exec(`
		INSERT INTO warp_regions (region, subnet, enabled) VALUES ($1,$2,$3)
		ON CONFLICT (region) DO UPDATE SET subnet = EXCLUDED.subnet, enabled = EXCLUDED.enabled`,
		region, subnet, enabled)
	return err
}

func (s *PostgresStore) DeleteWarpRegion(region string) error {
	_, err := s.db.Exec(`DELETE FROM warp_regions WHERE region = $1`, region)
	return err
}

// --- Leaders ---

func (s *PostgresStore) ListWarpLeaders() ([]WarpLeader, error) {
	rows, err := s.db.Query(`SELECT leader_id, region, endpoint, enabled, created_at FROM warp_leaders ORDER BY region, leader_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWarpLeaders(rows)
}

func (s *PostgresStore) ListWarpLeadersByRegion(region string) ([]WarpLeader, error) {
	rows, err := s.db.Query(`SELECT leader_id, region, endpoint, enabled, created_at FROM warp_leaders WHERE region = $1 ORDER BY leader_id`, region)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWarpLeaders(rows)
}

// UpsertWarpLeader inserts or updates a leader's region, endpoint + enabled flag.
func (s *PostgresStore) UpsertWarpLeader(leaderID, region, endpoint string, enabled bool) error {
	_, err := s.db.Exec(`
		INSERT INTO warp_leaders (leader_id, region, endpoint, enabled) VALUES ($1,$2,$3,$4)
		ON CONFLICT (leader_id) DO UPDATE SET region = EXCLUDED.region, endpoint = EXCLUDED.endpoint, enabled = EXCLUDED.enabled`,
		leaderID, region, endpoint, enabled)
	return err
}

func (s *PostgresStore) DeleteWarpLeader(leaderID string) error {
	_, err := s.db.Exec(`DELETE FROM warp_leaders WHERE leader_id = $1`, leaderID)
	return err
}

func scanWarpLeaders(rows *sql.Rows) ([]WarpLeader, error) {
	var out []WarpLeader
	for rows.Next() {
		var l WarpLeader
		if err := rows.Scan(&l.LeaderID, &l.Region, &l.Endpoint, &l.Enabled, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SeedWarpRegionIfEmpty seeds the registry with a single region + leader when no
// regions exist yet, so an existing single-hub deployment keeps working unchanged.
// The seed region id equals the old leader id ("leader-01") so the WG key derived
// from CLUSTER_SECRET+region stays byte-identical to the pre-multi-hub key.
func (s *PostgresStore) SeedWarpRegionIfEmpty(region, subnet, leaderID, endpoint string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM warp_regions`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if err := s.UpsertWarpRegion(region, subnet, true); err != nil {
		return err
	}
	if endpoint == "" {
		// No endpoint configured yet: still create the region, but skip the
		// leader row (operator sets the endpoint from the panel).
		return nil
	}
	return s.UpsertWarpLeader(leaderID, region, endpoint, true)
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
// The peer pins to `region`; the IP comes from that region's subnet (allocIP),
// but the taken-set is global because region subnets never overlap.
func (s *PostgresStore) EnrollPeerTx(keyID, limit int, onNewConn, pubkey, fixedIP, region string, allocIP func(taken map[string]bool) (string, error)) (wgIP string, evicted string, err error) {
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
		`INSERT INTO warp_peers (api_key_id, pubkey, wg_ip, region) VALUES ($1,$2,$3,$4)`,
		keyID, pubkey, wgIP, region); err != nil {
		return "", "", err
	}
	if err = tx.Commit(); err != nil {
		return "", "", err
	}
	return wgIP, evicted, nil
}

// GetWarpAPIKeyByNodeID returns the warp key for a link identity (node_id),
// revoked or not. Revoked rows are included so a revoke whose best-effort Redis
// teardown failed can be retried and finish; authentication never goes through
// this lookup (WarpAPIKeyMiddleware checks revoked_at on the hash lookup).
func (s *PostgresStore) GetWarpAPIKeyByNodeID(nodeID string) (*WarpAPIKey, error) {
	var k WarpAPIKey
	var fixedIP, node, region, ownerID sql.NullString
	err := s.db.QueryRow(`
		SELECT id, name, key_hash, policy, max_conns, on_new_conn,
		       COALESCE(fixed_wg_ip,''), COALESCE(node_id,''), COALESCE(region,''),
		       COALESCE(owner_id::text,''), revoked_at, created_at
		FROM warp_api_keys WHERE node_id = $1`, nodeID).
		Scan(&k.ID, &k.Name, &k.KeyHash, &k.Policy, &k.MaxConns, &k.OnNewConn,
			&fixedIP, &node, &region, &ownerID, &k.RevokedAt, &k.CreatedAt)
	if err != nil {
		return nil, err
	}
	k.FixedWGIP, k.NodeID, k.Region, k.OwnerID = fixedIP.String, node.String, region.String, ownerID.String
	return &k, nil
}

// RevokeWarpAPIKeyByNodeID marks a link kit's warp key revoked (revoked_at = NOW),
// blocking warp re-enrollment and any future link-boot for that identity.
func (s *PostgresStore) RevokeWarpAPIKeyByNodeID(nodeID string) error {
	_, err := s.db.Exec(`UPDATE warp_api_keys SET revoked_at = NOW() WHERE node_id = $1 AND revoked_at IS NULL`, nodeID)
	return err
}
