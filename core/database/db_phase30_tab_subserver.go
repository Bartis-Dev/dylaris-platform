package database

import (
	"database/sql"
	"fmt"
)

// applyTabSubServerSchema lets a custom tab belong to one sub-server.
//
// A proxied tab points at a PORT inside the container, and the container runs
// whichever sub-server is active. So a BlueMap tab on 8123 was really pointing
// at "whatever is running right now": switch the active sub-server and the same
// tab silently shows a different world's map, or nothing at all if that
// sub-server does not run a map. The tab was per server; the thing it addresses
// is per sub-server.
//
// Empty string means every sub-server, which is both the old behaviour and the
// right default - a tab for a plugin every world runs should not have to be
// created once per world. A named value pins the tab, and the proxy refuses to
// serve it while a different sub-server is active.
//
// Same column name and type as server_mods and server_modpack_contents, which
// already key on the sub-server, so the three read alike.
func applyTabSubServerSchema(db *sql.DB) error {
	for _, q := range []string{
		`ALTER TABLE server_tabs ADD COLUMN IF NOT EXISTS sub_server_name VARCHAR(128) NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("tab sub-server: alter server_tabs: %w", err)
		}
	}
	return nil
}
