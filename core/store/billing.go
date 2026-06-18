package store

import (
	"database/sql"
	"dylaris-core/models"
	"time"
)

// UserBilling is one tenant's billing/lifecycle state. A missing row means
// "active with no overrides" — the default for every user, so reads never error
// on not-found. Retention overrides are specs like "3d"/"2w"/"3m"; empty = use
// the platform default.
type UserBilling struct {
	UserID        string     `json:"userId"`
	Status        string     `json:"status"` // active | past_due | suspended
	GraceUntil    *time.Time `json:"graceUntil,omitempty"`
	SuspendedAt   *time.Time `json:"suspendedAt,omitempty"`
	GracePeriod   string     `json:"gracePeriod,omitempty"`
	R2Retention   string     `json:"r2Retention,omitempty"`
	NodeRetention string     `json:"nodeRetention,omitempty"`
	R2QuotaGB     *int64     `json:"r2QuotaGb,omitempty"` // per-user override; nil = platform default
	UpdatedAt     time.Time  `json:"updatedAt"`
}

func scanUserBilling(row interface {
	Scan(dest ...any) error
}) (*UserBilling, error) {
	var b UserBilling
	var grace, susp sql.NullTime
	var gp, r2, nr sql.NullString
	var quota sql.NullInt64
	if err := row.Scan(&b.UserID, &b.Status, &grace, &susp, &gp, &r2, &nr, &quota, &b.UpdatedAt); err != nil {
		return nil, err
	}
	if grace.Valid {
		b.GraceUntil = &grace.Time
	}
	if susp.Valid {
		b.SuspendedAt = &susp.Time
	}
	b.GracePeriod = gp.String
	b.R2Retention = r2.String
	b.NodeRetention = nr.String
	if quota.Valid {
		b.R2QuotaGB = &quota.Int64
	}
	return &b, nil
}

// GetUserBilling returns a tenant's billing row, or a zero-value active row when
// none exists.
func (s *PostgresStore) GetUserBilling(userID string) (*UserBilling, error) {
	row := s.db.QueryRow(`
		SELECT user_id, status, grace_until, suspended_at, grace_period, r2_retention, node_retention, r2_quota_gb, updated_at
		FROM user_billing WHERE user_id = $1`, userID)
	b, err := scanUserBilling(row)
	if err == sql.ErrNoRows {
		return &UserBilling{UserID: userID, Status: "active"}, nil
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}

// SetUserBillingStatus upserts the status + lifecycle timestamps, leaving the
// per-user retention overrides untouched.
func (s *PostgresStore) SetUserBillingStatus(userID, status string, graceUntil, suspendedAt *time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO user_billing (user_id, status, grace_until, suspended_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			status       = EXCLUDED.status,
			grace_until  = EXCLUDED.grace_until,
			suspended_at = EXCLUDED.suspended_at,
			updated_at   = NOW()`,
		userID, status, graceUntil, suspendedAt)
	return err
}

// SetUserBillingOverrides upserts the per-user retention overrides, leaving the
// status/timestamps untouched. An empty spec clears the override (NULL = default).
// r2QuotaGB is a pointer so the caller can distinguish "use platform default"
// (nil -> NULL) from an explicit 0 ("unlimited for this user").
func (s *PostgresStore) SetUserBillingOverrides(userID, gracePeriod, r2Retention, nodeRetention string, r2QuotaGB *int64) error {
	_, err := s.db.Exec(`
		INSERT INTO user_billing (user_id, grace_period, r2_retention, node_retention, r2_quota_gb, updated_at)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''), NULLIF($4,''), $5, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			grace_period   = NULLIF($2,''),
			r2_retention   = NULLIF($3,''),
			node_retention = NULLIF($4,''),
			r2_quota_gb    = $5,
			updated_at     = NOW()`,
		userID, gracePeriod, r2Retention, nodeRetention, r2QuotaGB)
	return err
}

// ListServersByOwner returns the servers a tenant owns (id, uuid, status,
// node_id), used by the billing lifecycle to stop a suspended tenant's servers
// without pulling the full server columns.
func (s *PostgresStore) ListServersByOwner(ownerID string) ([]models.Server, error) {
	rows, err := s.db.Query(`SELECT id, uuid, status, node_id FROM servers WHERE owner_id = $1`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Server
	for rows.Next() {
		var srv models.Server
		if err := rows.Scan(&srv.ID, &srv.UUID, &srv.Status, &srv.NodeID); err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

// BackupRunRef is the minimal handle the retention cleanup needs to delete a
// stored backup: the run row id, its storage object key, and which storage
// backend holds it.
type BackupRunRef struct {
	RunID      int
	StorageKey string
	StorageID  *int
}

// ListBackupRunsByOwner returns every backup run for the servers a tenant owns,
// used by the billing retention cleanup to delete R2 objects + rows after the
// r2_retention window.
func (s *PostgresStore) ListBackupRunsByOwner(ownerID string) ([]BackupRunRef, error) {
	rows, err := s.db.Query(`
		SELECT br.id, br.storage_key, bj.storage_id
		FROM backup_runs br
		JOIN backup_jobs bj ON bj.id = br.job_id
		JOIN servers s ON s.id = bj.server_id
		WHERE s.owner_id = $1`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupRunRef
	for rows.Next() {
		var ref BackupRunRef
		var sid sql.NullInt64
		if err := rows.Scan(&ref.RunID, &ref.StorageKey, &sid); err != nil {
			return nil, err
		}
		if sid.Valid {
			v := int(sid.Int64)
			ref.StorageID = &v
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// BackupBytesByOwner returns the current stored backup size (successful runs) for
// a tenant. Used by the R2 quota gate before a new backup.
func (s *PostgresStore) BackupBytesByOwner(ownerID string) (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRow(`
		SELECT COALESCE(SUM(br.size_bytes), 0)
		FROM backup_runs br
		JOIN backup_jobs bj ON bj.id = br.job_id
		JOIN servers s ON s.id = bj.server_id
		WHERE s.owner_id = $1 AND br.status = 'success'`, ownerID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// ListUserBillingByStatus returns every tenant in a given lifecycle status. Used
// by the leader-gated lifecycle worker to progress past_due -> suspended ->
// retention cleanup.
func (s *PostgresStore) ListUserBillingByStatus(status string) ([]UserBilling, error) {
	rows, err := s.db.Query(`
		SELECT user_id, status, grace_until, suspended_at, grace_period, r2_retention, node_retention, r2_quota_gb, updated_at
		FROM user_billing WHERE status = $1`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserBilling
	for rows.Next() {
		b, err := scanUserBilling(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}
