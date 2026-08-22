package database

import (
	"database/sql"
	"fmt"
)

// createCustomDomainTables owns the ownership-proof state for tenant custom
// domains.
//
// The state is keyed on (user_id, domain), never on the domain alone, and the
// UNIQUE constraint below is what enforces that. A global per-domain block would
// hand anyone a way to permanently lock a competitor's domain out of the
// platform: enter "theircompany.com", never set the CNAME, and the failure is
// recorded against the domain for everyone. Per user, a squatter only ever
// blocks themselves - and they were never going to pass the check anyway,
// because passing it requires DNS control.
func createCustomDomainTables(db *sql.DB) error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS custom_domain_claims (
			id SERIAL PRIMARY KEY,
			user_id TEXT NOT NULL,
			domain TEXT NOT NULL,
			-- pending | verified | blocked | permablocked
			state TEXT NOT NULL DEFAULT 'pending',
			-- Failed verification rounds. The second one is permanent.
			attempts INTEGER NOT NULL DEFAULT 0,
			-- When a pending claim stops being given the benefit of the doubt.
			deadline_at TIMESTAMPTZ,
			-- Self-service unblock token, issued only after a permanent block.
			txt_token TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (user_id, domain)
		)`,
		// The poller scans pending claims by deadline every 30 minutes.
		`CREATE INDEX IF NOT EXISTS idx_custom_domain_claims_pending
			ON custom_domain_claims (state, deadline_at)`,
	}
	for _, q := range tables {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("custom domain table creation error: %w", err)
		}
	}
	return nil
}
