package store

import (
	"database/sql"
	"time"
)

// TrafficUsage is one tenant's metered usage for one billing month. edge_bytes
// is the billable player traffic; relay_bytes and backup_bytes are tracked for
// observability and (later) overage. A zero-value row (all counters 0) is the
// correct answer for a tenant with no traffic yet, so reads never error on
// "not found".
type TrafficUsage struct {
	UserID      string    `json:"userId"`
	Period      time.Time `json:"period"` // first day of the billing month (UTC)
	EdgeBytes   int64     `json:"edgeBytes"`
	RelayBytes  int64     `json:"relayBytes"`
	BackupBytes int64     `json:"backupBytes"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// TenantServerOwners returns serverUUID -> owning tenant (user id) for every
// server that sits on a tenant-owned (BYON) node. Platform nodes (owner_id NULL)
// are excluded, so their traffic is never billed. The traffic aggregator builds
// this map once per tick to attribute per-server byte counters to a tenant.
func (s *PostgresStore) TenantServerOwners() (map[string]string, error) {
	rows, err := s.db.Query(`
		SELECT s.uuid, n.owner_id
		FROM servers s
		JOIN nodes n ON n.id = s.node_id
		WHERE n.owner_id IS NOT NULL AND s.uuid <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var uuid, owner string
		if err := rows.Scan(&uuid, &owner); err != nil {
			return nil, err
		}
		out[uuid] = owner
	}
	return out, rows.Err()
}

// TenantBackupBytes returns the current stored backup size per tenant (user id),
// summing successful backup runs for servers on tenant-owned nodes. This is a
// storage gauge (what is held in R2 right now), not a flow, so the aggregator
// overwrites the snapshot each tick. Tenants with no backups are absent from the
// map; the caller resets those to 0.
func (s *PostgresStore) TenantBackupBytes() (map[string]int64, error) {
	rows, err := s.db.Query(`
		SELECT n.owner_id, COALESCE(SUM(br.size_bytes), 0)
		FROM backup_runs br
		JOIN backup_jobs bj ON bj.id = br.job_id
		JOIN servers s ON s.id = bj.server_id
		JOIN nodes n ON n.id = s.node_id
		WHERE n.owner_id IS NOT NULL AND br.status = 'success'
		GROUP BY n.owner_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var owner string
		var total int64
		if err := rows.Scan(&owner, &total); err != nil {
			return nil, err
		}
		out[owner] = total
	}
	return out, rows.Err()
}

// AddTrafficUsage adds (not sets) byte deltas onto a tenant's current-period row,
// creating it on first write. The aggregator computes deltas from monotonic
// Redis counters, so this accumulates the running monthly total.
func (s *PostgresStore) AddTrafficUsage(userID string, period time.Time, edgeBytes, relayBytes int64) error {
	_, err := s.db.Exec(`
		INSERT INTO traffic_usage (user_id, period, edge_bytes, relay_bytes, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (user_id, period) DO UPDATE SET
			edge_bytes  = traffic_usage.edge_bytes  + EXCLUDED.edge_bytes,
			relay_bytes = traffic_usage.relay_bytes + EXCLUDED.relay_bytes,
			updated_at  = NOW()`,
		userID, period, edgeBytes, relayBytes)
	return err
}

// SetTrafficBackupBytes overwrites (not adds) the R2 backup-storage snapshot for
// a tenant's period. Storage is a gauge (current total), not a cumulative flow,
// so each aggregation tick replaces the value.
func (s *PostgresStore) SetTrafficBackupBytes(userID string, period time.Time, backupBytes int64) error {
	_, err := s.db.Exec(`
		INSERT INTO traffic_usage (user_id, period, backup_bytes, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, period) DO UPDATE SET
			backup_bytes = EXCLUDED.backup_bytes,
			updated_at   = NOW()`,
		userID, period, backupBytes)
	return err
}

// GetTrafficUsage returns one tenant's usage for a period. A missing row is not
// an error: it returns a zero-value usage so callers render 0 instead of failing.
func (s *PostgresStore) GetTrafficUsage(userID string, period time.Time) (*TrafficUsage, error) {
	u := &TrafficUsage{UserID: userID, Period: period}
	err := s.db.QueryRow(`
		SELECT edge_bytes, relay_bytes, backup_bytes, updated_at
		FROM traffic_usage WHERE user_id = $1 AND period = $2`,
		userID, period).Scan(&u.EdgeBytes, &u.RelayBytes, &u.BackupBytes, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return u, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// ListTrafficUsage returns every tenant's usage for a period, busiest first.
// Used by the admin usage overview.
func (s *PostgresStore) ListTrafficUsage(period time.Time) ([]TrafficUsage, error) {
	rows, err := s.db.Query(`
		SELECT user_id, period, edge_bytes, relay_bytes, backup_bytes, updated_at
		FROM traffic_usage WHERE period = $1
		ORDER BY edge_bytes DESC`, period)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrafficUsage
	for rows.Next() {
		var u TrafficUsage
		if err := rows.Scan(&u.UserID, &u.Period, &u.EdgeBytes, &u.RelayBytes, &u.BackupBytes, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
