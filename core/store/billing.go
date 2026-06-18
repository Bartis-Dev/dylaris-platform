package store

import (
	"database/sql"
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
	UpdatedAt     time.Time  `json:"updatedAt"`
}

func scanUserBilling(row interface {
	Scan(dest ...any) error
}) (*UserBilling, error) {
	var b UserBilling
	var grace, susp sql.NullTime
	var gp, r2, nr sql.NullString
	if err := row.Scan(&b.UserID, &b.Status, &grace, &susp, &gp, &r2, &nr, &b.UpdatedAt); err != nil {
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
	return &b, nil
}

// GetUserBilling returns a tenant's billing row, or a zero-value active row when
// none exists.
func (s *PostgresStore) GetUserBilling(userID string) (*UserBilling, error) {
	row := s.db.QueryRow(`
		SELECT user_id, status, grace_until, suspended_at, grace_period, r2_retention, node_retention, updated_at
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
func (s *PostgresStore) SetUserBillingOverrides(userID, gracePeriod, r2Retention, nodeRetention string) error {
	_, err := s.db.Exec(`
		INSERT INTO user_billing (user_id, grace_period, r2_retention, node_retention, updated_at)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''), NULLIF($4,''), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			grace_period   = NULLIF($2,''),
			r2_retention   = NULLIF($3,''),
			node_retention = NULLIF($4,''),
			updated_at     = NOW()`,
		userID, gracePeriod, r2Retention, nodeRetention)
	return err
}

// ListUserBillingByStatus returns every tenant in a given lifecycle status. Used
// by the leader-gated lifecycle worker to progress past_due -> suspended ->
// retention cleanup.
func (s *PostgresStore) ListUserBillingByStatus(status string) ([]UserBilling, error) {
	rows, err := s.db.Query(`
		SELECT user_id, status, grace_until, suspended_at, grace_period, r2_retention, node_retention, updated_at
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
