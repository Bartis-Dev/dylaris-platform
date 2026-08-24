package database

import (
	"database/sql"
	"fmt"
)

// applyTrafficBillingSchema records what the store has told us about a tenant's
// TRAFFIC deal, so the panel can warn them before it costs them their servers.
//
// Traffic is the only thing a tenant cannot cap in advance - a popular weekend
// moves more of it than anyone plans for - so metered billing is off until they
// switch it on, and the price of leaving it off is a stop at the fair-use
// ceiling rather than a bill. Core is not the one that decides any of that; the
// store owns the money. Core only needs the two numbers to say "you are at 84%
// of what your subscription covers, and nothing will be charged - it will stop".
//
//   - traffic_ceiling_gb: where free traffic ends, in DECIMAL GB (10^9), which is
//     how bandwidth is metered and what the store computes with. Deliberately NOT
//     folded into traffic_edge_gb: that one is a warn-only limit compared in GiB
//     (1024^3), and reusing it would silently move the threshold by 7%.
//   - traffic_billing_enabled: whether the tenant has agreed to be charged for
//     what is above it. Default false - the whole point is that consent is given,
//     not assumed.
//
// Both are nullable/defaulted and only ever written by the store, so an install
// with no store attached reads zeros and shows no banner. Additive + idempotent.
func applyTrafficBillingSchema(db *sql.DB) error {
	for _, q := range []string{
		`ALTER TABLE user_billing ADD COLUMN IF NOT EXISTS traffic_ceiling_gb BIGINT`,
		`ALTER TABLE user_billing ADD COLUMN IF NOT EXISTS traffic_billing_enabled BOOLEAN NOT NULL DEFAULT false`,
	} {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("traffic billing: alter user_billing: %w", err)
		}
	}
	return nil
}
