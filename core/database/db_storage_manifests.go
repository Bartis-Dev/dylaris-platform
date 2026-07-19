package database

import (
	"database/sql"
	"fmt"
)

// storageManifestsSchemaSQL returns the statements this phase issues, in
// order. Exposed as a function (rather than inlined) so a test can assert the
// phase contains nothing destructive.
func storageManifestsSchemaSQL() string {
	return storageManifestsTable + "\n" + storageManifestEntriesTable
}

const storageManifestsTable = `CREATE TABLE IF NOT EXISTS storage_manifests (
	id            SERIAL PRIMARY KEY,
	data_set      VARCHAR(128) NOT NULL,
	backend_label TEXT         NOT NULL,
	algo          VARCHAR(16)  NOT NULL,
	captured_at   TIMESTAMPTZ  NOT NULL,
	object_count  BIGINT       NOT NULL,
	total_bytes   BIGINT       NOT NULL,
	created_by    VARCHAR(64)  NOT NULL DEFAULT ''
)`

const storageManifestEntriesTable = `CREATE TABLE IF NOT EXISTS storage_manifest_entries (
	manifest_id INTEGER     NOT NULL REFERENCES storage_manifests(id) ON DELETE CASCADE,
	key         TEXT        NOT NULL,
	size        BIGINT      NOT NULL,
	checksum    VARCHAR(64) NOT NULL,
	PRIMARY KEY (manifest_id, key)
)`

// applyStorageManifestsSchema creates the storage-migration manifest tables:
//   - storage_manifests: one header per captured inventory of a blob data set
//   - storage_manifest_entries: key + size + sha256 per object
//
// Strictly ADDITIVE and idempotent - it drops nothing and alters nothing.
// backend_label is descriptive only and must never carry credentials; that is
// enforced by the writers in services/storagemigrate, not by the schema.
func applyStorageManifestsSchema(db *sql.DB) error {
	if _, err := db.Exec(storageManifestsTable); err != nil {
		return fmt.Errorf("storage manifests: create storage_manifests: %w", err)
	}
	if _, err := db.Exec(storageManifestEntriesTable); err != nil {
		return fmt.Errorf("storage manifests: create storage_manifest_entries: %w", err)
	}
	return nil
}
