package models

import (
	"encoding/json"
	"time"
)

// StorageConnection is a named, reusable storage backend (currently s3) that any
// feature can reference by id instead of re-entering credentials. Config holds
// only the non-secret fields (endpoint, region, bucket, forcePathStyle, prefix);
// the access key id is not a secret; the secret access key lives encrypted in
// its own column and never crosses the API.
type StorageConnection struct {
	ID        int             `json:"id"`
	Name      string          `json:"name"`
	Provider  string          `json:"provider"` // "s3"
	Config    json.RawMessage `json:"config"`
	AccessKey string          `json:"accessKey"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`

	// SecretAccessKey is the DECRYPTED secret, populated only by
	// GetStorageConnection for internal provider construction. json:"-"
	// guarantees it is never serialized, so it cannot leak over the API even if
	// a handler forgets to redact. On the write path a handler sets it from a
	// request DTO field (the model itself never decodes it, by the same tag).
	SecretAccessKey string `json:"-"`

	// SecretSet reports on a read response that a secret is stored WITHOUT
	// returning it, so the panel can show a write-only "leave blank to keep"
	// field. Transient: derived from the column on read, never persisted.
	SecretSet bool `json:"secretSet,omitempty"`
}
