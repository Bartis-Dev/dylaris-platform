package database

import (
	"database/sql"
	"fmt"
)

// storageConnectionsSchemaSQL returns the statements this phase issues, in
// order. Exposed as a function (rather than inlined) so a test can assert the
// phase contains nothing destructive.
func storageConnectionsSchemaSQL() string {
	return storageConnectionsTable
}

// storage_connections is a named, reusable storage backend (currently s3) that
// any feature can reference by id instead of re-entering credentials. The
// access key id is not a secret and is stored plainly; the secret access key
// lives encrypted in its OWN column (secret_enc, AES-256-GCM via
// pkg/crypto/at_rest.go), never inside the config JSONB, so a stray blob read
// can never surface it. config holds only the non-secret fields (endpoint,
// region, bucket, forcePathStyle, prefix).
const storageConnectionsTable = `CREATE TABLE IF NOT EXISTS storage_connections (
	id          SERIAL PRIMARY KEY,
	name        TEXT NOT NULL UNIQUE,
	provider    TEXT NOT NULL,
	config      JSONB NOT NULL DEFAULT '{}'::jsonb,
	access_key  TEXT NOT NULL DEFAULT '',
	secret_enc  TEXT NOT NULL DEFAULT '',
	created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
)`

// applyStorageConnectionsSchema creates the storage_connections table. Strictly
// ADDITIVE and idempotent - it drops nothing and alters nothing, so it is safe
// to run on every boot and against a fresh migration-target DB.
func applyStorageConnectionsSchema(db *sql.DB) error {
	if _, err := db.Exec(storageConnectionsTable); err != nil {
		return fmt.Errorf("storage connections: create storage_connections: %w", err)
	}
	return nil
}
