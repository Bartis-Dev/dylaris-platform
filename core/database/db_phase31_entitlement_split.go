package database

import (
	"database/sql"
	"fmt"
)

// applyEntitlementSplitSchema gives BYON and route-only their own expiry, so an
// admin can hold both at once.
//
// They used to share one row: a single `manual_entitlement` string ("byon" |
// "route_only" | "both") and a single expiry. "both" existed, but the two were
// still ONE grant - so granting route-only to a tenant who already had BYON
// replaced it rather than adding to it, and the only way to hold both was to
// know in advance and pick "both" from a dropdown. Reported from BYON testing as
// "it just switches between the two", which is exactly what it did.
//
// Two nullable expiries are now the truth, one per kind. The old string column
// stays and is kept in step, because the partial index below is defined on it
// and because an older Core reading this row must still see something sensible
// rather than an empty grant.
//
// Backfill mirrors the old semantics exactly: whichever kinds the string named
// get the old expiry, the other stays NULL. A row with no grant gets neither.
//
// Additive + idempotent.
func applyEntitlementSplitSchema(db *sql.DB) error {
	for _, q := range []string{
		`ALTER TABLE user_billing ADD COLUMN IF NOT EXISTS manual_entitlement_byon_expires_at TIMESTAMPTZ`,
		`ALTER TABLE user_billing ADD COLUMN IF NOT EXISTS manual_entitlement_route_expires_at TIMESTAMPTZ`,
	} {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("entitlement split: alter user_billing: %w", err)
		}
	}

	// Backfill only where the new columns are still untouched. Guarded on IS NULL
	// rather than run unconditionally: this migration runs on every boot, and a
	// grant edited after the upgrade must not be reset to whatever the legacy
	// string still says.
	for _, q := range []string{
		`UPDATE user_billing
		    SET manual_entitlement_byon_expires_at = manual_entitlement_expires_at
		  WHERE manual_entitlement_byon_expires_at IS NULL
		    AND manual_entitlement IN ('byon', 'both')`,
		`UPDATE user_billing
		    SET manual_entitlement_route_expires_at = manual_entitlement_expires_at
		  WHERE manual_entitlement_route_expires_at IS NULL
		    AND manual_entitlement IN ('route_only', 'both')`,
	} {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("entitlement split: backfill: %w", err)
		}
	}
	return nil
}
