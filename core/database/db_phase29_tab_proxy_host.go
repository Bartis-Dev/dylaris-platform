package database

import (
	"database/sql"
	"fmt"
)

// applyTabProxyHostSchema gives every proxied custom tab its own hostname label.
//
// The tab proxy used to serve a container under a path prefix
// (/api/servers/{id}/tabs/{tabId}/proxy/) and rewrite the HTML with a
// <base href>. That only fixes RELATIVE urls: a path-absolute "/js/app.js" is
// resolved against the origin ROOT and ignores the base path, which is exactly
// what BlueMap and Dynmap emit. Both are unusable behind a prefix, and they are
// the two most deployed map plugins. Serving each tab at the ROOT of its own
// host is what makes them resolve.
//
// The label is a separate column rather than a reuse of share_token, because
// share_token is base62 INCLUDING uppercase and DNS labels are case-insensitive:
// two tokens differing only in case would collapse onto one hostname and the
// proxy could not tell them apart. That is a cross-tenant confusion, so the
// routing key gets its own lowercase alphabet and its own uniqueness guarantee.
//
// Nullable because a "direct" tab has no content host and needs none. The unique
// index is partial for the same reason - several direct tabs would otherwise
// collide on NULL under some engines, and a partial index states the intent.
func applyTabProxyHostSchema(db *sql.DB) error {
	for _, q := range []string{
		`ALTER TABLE server_tabs ADD COLUMN IF NOT EXISTS proxy_host_label VARCHAR(63)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_server_tabs_proxy_host_label
		   ON server_tabs (proxy_host_label) WHERE proxy_host_label IS NOT NULL`,
	} {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("tab proxy host: alter server_tabs: %w", err)
		}
	}
	return nil
}
