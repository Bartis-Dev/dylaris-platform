package database

import (
	"database/sql"
	"fmt"
	"log"
)

// legacyRegistryPrefix is where every image lived before the project moved from
// a personal GitHub account to the dylaris-dev organisation (2026-09-06).
const (
	legacyRegistryPrefix  = "ghcr.io/bartis-dev/dylaris-"
	currentRegistryPrefix = "ghcr.io/dylaris-dev/"
)

// applyRegistryMoveSchema rewrites the stored Minecraft runtime image of every
// server that still names the old registry path.
//
// This is not cosmetic and it is not optional. servers.game_image holds the
// FULL image reference - "ghcr.io/bartis-dev/dylaris-platform-mc-java21:latest",
// not a tag or a key - and it is read on every start and reinstall. Changing the
// default in code only affects servers created afterwards; every row written
// before the move keeps pointing at packages under an account that no longer
// builds them.
//
// The failure that makes this urgent is silent and delayed. Nothing breaks at
// deploy: a container that is already running keeps running, and the panel shows
// nothing wrong. It breaks the next time a server's container is recreated -
// a restart, a resource change, a migration to another node - and only for the
// servers that happen to be touched, at whatever hour that happens. Once the old
// packages are deleted, the pull fails outright.
//
// Prefix rewrite rather than a lookup table, deliberately: the old names are
// "dylaris-platform-mc-java8|17|21|25" and the new ones are the same strings
// with the redundant "dylaris-" dropped, because the organisation now carries
// that word. A table would have to be kept in step with every future runtime
// image; the prefix rule does not.
//
// Idempotent by construction - after the first run no row matches the LIKE - so
// it is safe on the boot-time migration path, and it reports how many rows it
// touched because "0" and "47" are very different things to see in a deploy log.
func applyRegistryMoveSchema(db *sql.DB) error {
	// The ::text casts are not decoration. The header of
	// postgres_integration_test.go lists "inconsistent types deduced for
	// parameter $1" among the defects that only a real Postgres rejected, and a
	// bare parameter inside length() and LIKE is exactly that shape. Naming the
	// type removes the inference step rather than hoping it lands.
	res, err := db.Exec(
		`UPDATE servers
		    SET game_image = $2::text || substring(game_image from length($1::text) + 1)
		  WHERE game_image LIKE $1::text || '%'`,
		legacyRegistryPrefix, currentRegistryPrefix,
	)
	if err != nil {
		return fmt.Errorf("registry move: rewrite servers.game_image: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		log.Printf("registry move: rewrote game_image on %d server(s) from %s to %s",
			n, legacyRegistryPrefix, currentRegistryPrefix)
	}
	return nil
}
