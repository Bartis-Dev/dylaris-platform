package handlers

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"dylaris-core/models"
)

// keyCoreStorageConnectionID names the setting that, when set to a storage
// connection id, makes Core file storage build from that named connection
// instead of the inline core_storage_s3_* keys. Empty or "0" means "use the
// inline config" - the fallback the spec keeps open.
const keyCoreStorageConnectionID = "core_storage_connection_id"

// selectedStorageConnection resolves the connection referenced by the given
// id-setting. ok is false (with a nil connection and nil error) when nothing is
// selected, so a caller falls back to its inline config. A selected-but-broken
// reference (non-numeric id, or a connection that no longer loads) is an ERROR,
// not a silent fallback: the operator chose a connection, so quietly building
// from possibly-stale inline config would hide the misconfiguration.
func (s *AppState) selectedStorageConnection(idSetting string) (*models.StorageConnection, bool, error) {
	if s.Store == nil {
		return nil, false, nil
	}
	raw, _ := s.Store.GetSetting(idSetting)
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return nil, false, nil
	}
	id, err := strconv.Atoi(raw)
	if err != nil {
		return nil, false, fmt.Errorf("storage connection: invalid id %q in %s: %w", raw, idSetting, err)
	}
	conn, err := s.Store.GetStorageConnection(id)
	if err != nil {
		return nil, false, fmt.Errorf("storage connection %d (%s) could not be loaded: %w", id, idSetting, err)
	}
	return conn, true, nil
}

// coreStorageConfigFromConnection maps a resolved connection (its secret already
// decrypted into SecretAccessKey by the store) onto a CoreStorageConfig. The
// backend is always s3; the access key and secret come from their own columns,
// the rest from the connection's config JSONB.
func coreStorageConfigFromConnection(conn *models.StorageConnection) CoreStorageConfig {
	var c storageConnectionConfig
	if len(conn.Config) > 0 {
		_ = json.Unmarshal(conn.Config, &c)
	}
	return CoreStorageConfig{
		Backend:     "s3",
		S3Endpoint:  c.Endpoint,
		S3Region:    c.Region,
		S3Bucket:    c.Bucket,
		S3AccessKey: conn.AccessKey,
		S3SecretKey: conn.SecretAccessKey,
		S3PathStyle: c.ForcePathStyle,
		S3Prefix:    c.Prefix,
		S3SecretSet: conn.SecretAccessKey != "",
	}
}

// effectiveCoreStorageConfig returns the config Core file storage is actually
// built from. A selected storage connection overrides the inline config
// entirely; otherwise the inline config is returned unchanged. This is the
// single place the "connection overrides inline" rule lives, so the provider
// builder, the gate sync and the configured-check all agree.
func (s *AppState) effectiveCoreStorageConfig() (CoreStorageConfig, error) {
	conn, ok, err := s.selectedStorageConnection(keyCoreStorageConnectionID)
	if err != nil {
		return CoreStorageConfig{}, err
	}
	if ok {
		return coreStorageConfigFromConnection(conn), nil
	}
	return s.LoadCoreStorageConfig(), nil
}
