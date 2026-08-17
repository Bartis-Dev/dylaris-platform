package database

import (
	"database/sql"
	"fmt"
)

// applyEntitlementSchema introduces "what is this tenant allowed to use" as a
// thing the platform can state, plus a time-boxed manual grant for it.
//
// Before this, there was no such concept. Plans capped QUANTITIES (max_nodes,
// max_links) and EffectiveLimits treats 0 as unlimited, so with BYON enabled a
// user with no plan - or a plan whose caps are 0 - could enroll unlimited nodes.
// There was nothing to ask "may this user do BYON at all", which is what both the
// tenant UI and a manual grant need.
//
//   - plans.kind: what a plan grants. Defaults to 'both' precisely so this
//     migration changes NO existing behaviour: every current plan keeps allowing
//     everything, exactly as it does today. Narrow it deliberately per plan.
//   - user_billing.manual_entitlement (+ expiry, and who granted it when): an
//     admin-granted entitlement that stands on its own. Additive by design - a
//     later store purchase simply extends the answer rather than conflicting with
//     it, which is what "give them 14 days now, they can subscribe later" needs.
//
// Additive + idempotent.
func applyEntitlementSchema(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE plans ADD COLUMN IF NOT EXISTS kind VARCHAR(16) NOT NULL DEFAULT 'both'`); err != nil {
		return fmt.Errorf("entitlements: add plans.kind: %w", err)
	}
	for _, q := range []string{
		`ALTER TABLE user_billing ADD COLUMN IF NOT EXISTS manual_entitlement VARCHAR(16) NOT NULL DEFAULT ''`,
		`ALTER TABLE user_billing ADD COLUMN IF NOT EXISTS manual_entitlement_expires_at TIMESTAMPTZ`,
		`ALTER TABLE user_billing ADD COLUMN IF NOT EXISTS manual_entitlement_granted_at TIMESTAMPTZ`,
		// SET NULL, not CASCADE: deleting the admin who granted something must not
		// delete the grant itself. Same reasoning as the invite-attribution fix.
		`ALTER TABLE user_billing ADD COLUMN IF NOT EXISTS manual_entitlement_granted_by UUID REFERENCES users(id) ON DELETE SET NULL`,
	} {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("entitlements: alter user_billing: %w", err)
		}
	}
	// Partial index: the expiry sweep and the "who has an active grant" admin view
	// only ever look at rows that actually carry one.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_user_billing_manual_entitlement
		ON user_billing(manual_entitlement_expires_at) WHERE manual_entitlement <> ''`); err != nil {
		return fmt.Errorf("entitlements: index manual_entitlement: %w", err)
	}
	return nil
}
