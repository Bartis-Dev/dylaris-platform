package store

import (
	"database/sql"
	"time"
)

// Plan is an admin-defined BYON tier. 0 means unlimited for every limit. Traffic
// limits are monthly and WARN-only (edge + relay tracked separately, combined is
// edge+relay). A plan's value is the baseline; a per-user override in user_billing
// wins (see services.EffectiveLimits).
type Plan struct {
	ID                int       `json:"id"`
	Name              string    `json:"name"`
	PriceLabel        string    `json:"priceLabel"`
	MaxNodes          int64     `json:"maxNodes"`
	R2QuotaGB         int64     `json:"r2QuotaGb"`
	TrafficEdgeGB     int64     `json:"trafficEdgeGb"`
	TrafficRelayGB    int64     `json:"trafficRelayGb"`
	TrafficCombinedGB int64     `json:"trafficCombinedGb"`
	IsDefault         bool      `json:"isDefault"`
	CreatedAt         time.Time `json:"createdAt"`
}

const planCols = `id, name, price_label, max_nodes, r2_quota_gb, traffic_edge_gb, traffic_relay_gb, traffic_combined_gb, is_default, created_at`

func scanPlan(row interface{ Scan(dest ...any) error }) (*Plan, error) {
	var p Plan
	if err := row.Scan(&p.ID, &p.Name, &p.PriceLabel, &p.MaxNodes, &p.R2QuotaGB,
		&p.TrafficEdgeGB, &p.TrafficRelayGB, &p.TrafficCombinedGB, &p.IsDefault, &p.CreatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *PostgresStore) ListPlans() ([]Plan, error) {
	rows, err := s.db.Query(`SELECT ` + planCols + ` FROM plans ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetPlan(id int) (*Plan, error) {
	return scanPlan(s.db.QueryRow(`SELECT `+planCols+` FROM plans WHERE id = $1`, id))
}

// GetDefaultPlan returns the default plan, or nil when none is set (sql.ErrNoRows
// is swallowed to nil so callers can treat "no default" as "unlimited").
func (s *PostgresStore) GetDefaultPlan() (*Plan, error) {
	p, err := scanPlan(s.db.QueryRow(`SELECT ` + planCols + ` FROM plans WHERE is_default LIMIT 1`))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// CreatePlan inserts a plan. When isDefault, it clears any existing default first
// (single-default invariant, also guarded by a partial unique index).
func (s *PostgresStore) CreatePlan(p Plan) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if p.IsDefault {
		if _, err = tx.Exec(`UPDATE plans SET is_default = FALSE WHERE is_default`); err != nil {
			return 0, err
		}
	}
	var id int
	err = tx.QueryRow(`
		INSERT INTO plans (name, price_label, max_nodes, r2_quota_gb, traffic_edge_gb, traffic_relay_gb, traffic_combined_gb, is_default)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		p.Name, p.PriceLabel, p.MaxNodes, p.R2QuotaGB, p.TrafficEdgeGB, p.TrafficRelayGB, p.TrafficCombinedGB, p.IsDefault).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// UpdatePlan updates a plan in place, honoring the single-default invariant.
func (s *PostgresStore) UpdatePlan(p Plan) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if p.IsDefault {
		if _, err = tx.Exec(`UPDATE plans SET is_default = FALSE WHERE is_default AND id <> $1`, p.ID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`
		UPDATE plans SET name=$2, price_label=$3, max_nodes=$4, r2_quota_gb=$5,
			traffic_edge_gb=$6, traffic_relay_gb=$7, traffic_combined_gb=$8, is_default=$9
		WHERE id=$1`,
		p.ID, p.Name, p.PriceLabel, p.MaxNodes, p.R2QuotaGB, p.TrafficEdgeGB, p.TrafficRelayGB, p.TrafficCombinedGB, p.IsDefault); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePlan removes a plan. users.plan_id is ON DELETE SET NULL-free (plain FK),
// so callers should ensure no user references it, or rely on the resolver falling
// back to the default plan once the row is gone (GetPlan errors -> default).
func (s *PostgresStore) DeletePlan(id int) error {
	_, err := s.db.Exec(`DELETE FROM plans WHERE id = $1`, id)
	return err
}

// GetUserPlanID returns the user's plan_id, or nil when unset (-> default plan).
func (s *PostgresStore) GetUserPlanID(userID string) (*int, error) {
	var pid sql.NullInt64
	if err := s.db.QueryRow(`SELECT plan_id FROM users WHERE id = $1`, userID).Scan(&pid); err != nil {
		return nil, err
	}
	if !pid.Valid {
		return nil, nil
	}
	v := int(pid.Int64)
	return &v, nil
}

// SetUserPlan assigns (planID non-nil) or clears (nil -> default plan) a user's plan.
func (s *PostgresStore) SetUserPlan(userID string, planID *int) error {
	_, err := s.db.Exec(`UPDATE users SET plan_id = $1 WHERE id = $2`, planID, userID)
	return err
}

// SetUserLimitOverrides upserts the per-user LIMIT overrides in user_billing
// (nil clears one -> use the plan value). It does NOT touch r2_quota_gb (owned by
// SetUserBillingOverrides) or the lifecycle/retention columns.
func (s *PostgresStore) SetUserLimitOverrides(userID string, maxNodes, trafficEdge, trafficRelay, trafficCombined *int64) error {
	_, err := s.db.Exec(`
		INSERT INTO user_billing (user_id, max_nodes, traffic_edge_gb, traffic_relay_gb, traffic_combined_gb, updated_at)
		VALUES ($1,$2,$3,$4,$5,NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			max_nodes = $2, traffic_edge_gb = $3, traffic_relay_gb = $4,
			traffic_combined_gb = $5, updated_at = NOW()`,
		userID, maxNodes, trafficEdge, trafficRelay, trafficCombined)
	return err
}

// CountNodesByOwner returns how many nodes a tenant owns (for the max_nodes gate).
func (s *PostgresStore) CountNodesByOwner(ownerID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE owner_id = $1`, ownerID).Scan(&n)
	return n, err
}
