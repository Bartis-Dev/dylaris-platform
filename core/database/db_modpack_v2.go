package database

import (
	"database/sql"
	"fmt"
)

// applyUnifiedModpackSchema creates the unified pack model that replaces the
// Phase 14/16 modpacks* tables. It is additive and idempotent; the old tables
// are dropped separately in the demolition step once all code has moved over.
func applyUnifiedModpackSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS packs (
			id                    SERIAL PRIMARY KEY,
			owner_id              UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			internal_name         VARCHAR(128) NOT NULL,
			internal_slug         VARCHAR(128) NOT NULL,
			summary               VARCHAR(512) NOT NULL DEFAULT '',
			solder_display_name   VARCHAR(128) NOT NULL DEFAULT '',
			solder_slug           VARCHAR(128) NOT NULL DEFAULT '',
			hidden                BOOLEAN      NOT NULL DEFAULT FALSE,
			private               BOOLEAN      NOT NULL DEFAULT FALSE,
			recommended_build     VARCHAR(64)  NOT NULL DEFAULT '',
			latest_build          VARCHAR(64)  NOT NULL DEFAULT '',
			icon_url              TEXT         NOT NULL DEFAULT '',
			logo_url              TEXT         NOT NULL DEFAULT '',
			background_url        TEXT         NOT NULL DEFAULT '',
			icon_md5              VARCHAR(32)  NOT NULL DEFAULT '',
			logo_md5              VARCHAR(32)  NOT NULL DEFAULT '',
			background_md5        VARCHAR(32)  NOT NULL DEFAULT '',
			modrinth_project_id   VARCHAR(64)  NOT NULL DEFAULT '',
			modrinth_project_name VARCHAR(128) NOT NULL DEFAULT '',
			modrinth_visibility   VARCHAR(16)  NOT NULL DEFAULT 'unlisted',
			created_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			UNIQUE (owner_id, internal_slug)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS packs_solder_slug_uniq ON packs (solder_slug) WHERE solder_slug <> ''`,
		`CREATE TABLE IF NOT EXISTS pack_builds (
			id                  SERIAL PRIMARY KEY,
			pack_id             INTEGER      NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
			version_string      VARCHAR(64)  NOT NULL,
			minecraft           VARCHAR(32)  NOT NULL DEFAULT '',
			loader              VARCHAR(32)  NOT NULL DEFAULT '',
			loader_version      VARCHAR(64)  NOT NULL DEFAULT '',
			min_java            VARCHAR(16)  NOT NULL DEFAULT '',
			min_memory          INTEGER      NOT NULL DEFAULT 0,
			changelog           TEXT         NOT NULL DEFAULT '',
			channel             VARCHAR(16)  NOT NULL DEFAULT 'draft',
			frozen              BOOLEAN      NOT NULL DEFAULT FALSE,
			solder_published    BOOLEAN      NOT NULL DEFAULT FALSE,
			solder_private      BOOLEAN      NOT NULL DEFAULT FALSE,
			modrinth_published  BOOLEAN      NOT NULL DEFAULT FALSE,
			modrinth_version_id VARCHAR(64)  NOT NULL DEFAULT '',
			mrpack_storage_key  VARCHAR(512) NOT NULL DEFAULT '',
			mrpack_sha256       VARCHAR(64)  NOT NULL DEFAULT '',
			created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			published_at        TIMESTAMPTZ,
			UNIQUE (pack_id, version_string)
		)`,
		`CREATE TABLE IF NOT EXISTS mods (
			id           SERIAL PRIMARY KEY,
			owner_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			slug         VARCHAR(128) NOT NULL,
			pretty_name  VARCHAR(255) NOT NULL DEFAULT '',
			author       VARCHAR(255) NOT NULL DEFAULT '',
			description  TEXT         NOT NULL DEFAULT '',
			link         TEXT         NOT NULL DEFAULT '',
			content_type VARCHAR(16)  NOT NULL DEFAULT 'mod',
			UNIQUE (owner_id, slug)
		)`,
		`CREATE TABLE IF NOT EXISTS modversions (
			id                         SERIAL PRIMARY KEY,
			mod_id                     INTEGER      NOT NULL REFERENCES mods(id) ON DELETE CASCADE,
			version                    VARCHAR(64)  NOT NULL DEFAULT '',
			filesize                   BIGINT       NOT NULL DEFAULT 0,
			storage_key                VARCHAR(512) NOT NULL DEFAULT '',
			md5                        VARCHAR(32)  NOT NULL DEFAULT '',
			sha1                       VARCHAR(40)  NOT NULL DEFAULT '',
			sha512                     VARCHAR(128) NOT NULL DEFAULT '',
			url_override               TEXT         NOT NULL DEFAULT '',
			source                     VARCHAR(16)  NOT NULL DEFAULT 'upload',
			target_path                VARCHAR(512) NOT NULL DEFAULT '',
			modrinth_project_id        VARCHAR(64)  NOT NULL DEFAULT '',
			modrinth_version_id        VARCHAR(64)  NOT NULL DEFAULT '',
			modrinth_version_number    VARCHAR(64)  NOT NULL DEFAULT '',
			modrinth_game_versions     TEXT         NOT NULL DEFAULT '',
			modrinth_latest_version_id VARCHAR(64)  NOT NULL DEFAULT '',
			modrinth_last_checked      TIMESTAMPTZ,
			created_at                 TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at                 TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS modversions_sha1_idx ON modversions (sha1) WHERE sha1 <> ''`,
		`CREATE TABLE IF NOT EXISTS build_modversions (
			id            SERIAL PRIMARY KEY,
			build_id      INTEGER     NOT NULL REFERENCES pack_builds(id) ON DELETE CASCADE,
			modversion_id INTEGER     NOT NULL REFERENCES modversions(id) ON DELETE CASCADE,
			side          VARCHAR(8)  NOT NULL DEFAULT 'both',
			UNIQUE (build_id, modversion_id)
		)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("unified modpack schema: %w", err)
		}
	}

	// One-time cleanup of the retired Phase 14/16 modpack tables. IF EXISTS
	// makes this a no-op after the first boot; the unified tables above are
	// the replacement. Clean-slate: no data migration by design.
	for _, drop := range []string{
		`DROP TABLE IF EXISTS modpack_mods CASCADE`,
		`DROP TABLE IF EXISTS modpack_versions CASCADE`,
		`DROP TABLE IF EXISTS modpacks CASCADE`,
	} {
		if _, err := db.Exec(drop); err != nil {
			return fmt.Errorf("unified modpack schema drop: %w", err)
		}
	}
	return nil
}
