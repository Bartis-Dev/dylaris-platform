package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

// ErrPlanInUse means users are still assigned to this plan. users.plan_id is
// REFERENCES plans(id) with no ON DELETE clause, so Postgres refuses rather than
// leaving accounts pointing at a plan that no longer exists. Typed for the same
// reason as ErrUserOwnsServers and ErrNodeHasServers: a bare 500 tells an
// operator nothing about what to do next.
var ErrPlanInUse = errors.New("plan is still assigned to users")

// Plan is an admin-defined BYON tier. 0 means unlimited for every limit. Traffic
// limits are monthly and WARN-only (edge + relay tracked separately, combined is
// edge+relay). A plan's value is the baseline; a per-user override in user_billing
// wins (see services.EffectiveLimits).
type Plan struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	PriceLabel        string `json:"priceLabel"`
	MaxNodes          int64  `json:"maxNodes"`
	MaxLinks          int64  `json:"maxLinks"`
	R2QuotaGB         int64  `json:"r2QuotaGb"`
	TrafficEdgeGB     int64  `json:"trafficEdgeGb"`
	TrafficRelayGB    int64  `json:"trafficRelayGb"`
	TrafficCombinedGB int64  `json:"trafficCombinedGb"`
	IsDefault         bool   `json:"isDefault"`
	// Kind is WHAT the plan grants: "byon" | "route_only" | "both". Defaults to
	// "both" so existing plans keep allowing everything (see the entitlement
	// migration); narrow it per plan deliberately. An unrecognised value grants
	// nothing - services.kindGrants fails closed rather than open.
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
}

const planCols = `id, name, price_label, max_nodes, max_links, r2_quota_gb, traffic_edge_gb, traffic_relay_gb, traffic_combined_gb, is_default, kind, created_at`

// normalizePlanKind keeps an unknown or empty kind from being written. Empty
// means "the caller did not say", and the behaviour-preserving answer there is
// "both" - the same default the column carries - not "nothing".
func normalizePlanKind(kind string) string {
	switch kind {
	case "byon", "route_only", "both":
		return kind
	default:
		return "both"
	}
}

func scanPlan(row interface{ Scan(dest ...any) error }) (*Plan, error) {
	var p Plan
	if err := row.Scan(&p.ID, &p.Name, &p.PriceLabel, &p.MaxNodes, &p.MaxLinks, &p.R2QuotaGB,
		&p.TrafficEdgeGB, &p.TrafficRelayGB, &p.TrafficCombinedGB, &p.IsDefault, &p.Kind, &p.CreatedAt); err != nil {
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
		INSERT INTO plans (name, price_label, max_nodes, max_links, r2_quota_gb, traffic_edge_gb, traffic_relay_gb, traffic_combined_gb, is_default, kind)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		p.Name, p.PriceLabel, p.MaxNodes, p.MaxLinks, p.R2QuotaGB, p.TrafficEdgeGB, p.TrafficRelayGB, p.TrafficCombinedGB, p.IsDefault, normalizePlanKind(p.Kind)).Scan(&id)
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
		UPDATE plans SET name=$2, price_label=$3, max_nodes=$4, max_links=$5, r2_quota_gb=$6,
			traffic_edge_gb=$7, traffic_relay_gb=$8, traffic_combined_gb=$9, is_default=$10, kind=$11
		WHERE id=$1`,
		p.ID, p.Name, p.PriceLabel, p.MaxNodes, p.MaxLinks, p.R2QuotaGB, p.TrafficEdgeGB, p.TrafficRelayGB, p.TrafficCombinedGB, p.IsDefault, normalizePlanKind(p.Kind)); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePlan removes a plan. users.plan_id is ON DELETE SET NULL-free (plain FK),
// so callers should ensure no user references it, or rely on the resolver falling
// back to the default plan once the row is gone (GetPlan errors -> default).
func (s *PostgresStore) DeletePlan(id int) error {
	_, err := s.db.Exec(`DELETE FROM plans WHERE id = $1`, id)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23503" && pqErr.Constraint == "users_plan_id_fkey" {
			return ErrPlanInUse
		}
	}
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
func (s *PostgresStore) SetUserLimitOverrides(userID string, maxNodes, maxLinks, trafficEdge, trafficRelay, trafficCombined *int64) error {
	_, err := s.db.Exec(`
		INSERT INTO user_billing (user_id, max_nodes, max_links, traffic_edge_gb, traffic_relay_gb, traffic_combined_gb, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			max_nodes = $2, max_links = $3, traffic_edge_gb = $4, traffic_relay_gb = $5,
			traffic_combined_gb = $6, updated_at = NOW()`,
		userID, maxNodes, maxLinks, trafficEdge, trafficRelay, trafficCombined)
	return err
}

// SetUserPurchasedEntitlement upserts ONLY the node/link overrides, and only for
// the dimensions the caller actually named. The store is the source of truth for
// what a tenant BOUGHT (a node count, and routes on a manual grant); an admin may
// independently have set traffic or R2 overrides, and a purchase must not wipe
// those. Passing set=false for a dimension leaves that column exactly as it was,
// which is why this cannot reuse SetUserLimitOverrides (that one owns all five
// columns and writes NULL for anything the PATCH body omitted).
//
// A nil value with set=true CLEARS the override, dropping the tenant back to the
// plan baseline - that is how a subscription ending returns them to free limits.
func (s *PostgresStore) SetUserPurchasedEntitlement(userID string, maxNodes *int64, setNodes bool, maxLinks *int64, setLinks bool) error {
	_, err := s.db.Exec(`
		INSERT INTO user_billing (user_id, max_nodes, max_links, updated_at)
		VALUES ($1,$2,$3,NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			max_nodes  = CASE WHEN $4::bool THEN $2 ELSE user_billing.max_nodes END,
			max_links  = CASE WHEN $5::bool THEN $3 ELSE user_billing.max_links END,
			updated_at = NOW()`,
		userID, maxNodes, maxLinks, setNodes, setLinks)
	return err
}

// SetUserTrafficBilling records what the store has decided about this tenant's
// traffic: where their free allowance ends and whether they have agreed to be
// charged past it. Touches ONLY those two columns - the row also carries the
// payment status and every limit override, and a provision call must not rewrite
// either. Core never decides these; it reads them to warn the tenant.
func (s *PostgresStore) SetUserTrafficBilling(userID string, ceilingGB int64, enabled bool) error {
	_, err := s.db.Exec(`
		INSERT INTO user_billing (user_id, traffic_ceiling_gb, traffic_billing_enabled, updated_at)
		VALUES ($1,$2,$3,NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			traffic_ceiling_gb      = $2,
			traffic_billing_enabled = $3,
			updated_at              = NOW()`,
		userID, ceilingGB, enabled)
	return err
}

// CountNodesByOwner returns how many nodes a tenant owns (for the max_nodes gate).
func (s *PostgresStore) CountNodesByOwner(ownerID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE owner_id = $1`, ownerID).Scan(&n)
	return n, err
}

// CountNodeWarpKeysByOwner returns how many UNREDEEMED BYON node warp keys a
// tenant holds. Counted against max_nodes alongside CountNodesByOwner, because a
// minted key is a node that has not connected YET - capping only on nodes that
// already exist would let a tenant with a one-node plan mint keys without limit
// and stand up as many machines as they liked.
func (s *PostgresStore) CountNodeWarpKeysByOwner(ownerID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM warp_api_keys
		WHERE owner_id = $1::uuid AND revoked_at IS NULL AND node_id LIKE 'node-%'`, ownerID).Scan(&n)
	return n, err
}

// CountLinkKitsByOwner returns how many route-only link kits a tenant owns (for the
// max_links gate). warp_api_keys also holds BYON node keys, so this filters to the
// 'link-' node_id prefix.
func (s *PostgresStore) CountLinkKitsByOwner(ownerID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM warp_api_keys
		WHERE owner_id = $1::uuid AND revoked_at IS NULL AND node_id LIKE 'link-%'`, ownerID).Scan(&n)
	return n, err
}
