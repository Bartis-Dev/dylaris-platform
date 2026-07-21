package store

import (
	"encoding/json"
	"errors"
	"log"

	"dylaris-core/pkg/crypto"
)

// backupStorageSecretPurpose scopes the at-rest key derivation for backup
// storage secrets, so it is distinct from every other CLUSTER_SECRET-derived
// key (node secrets, settings secrets, storage-connection secrets).
const backupStorageSecretPurpose = "backup-storage-secret"

// backupStorageSecretField is the one credential field inside an s3 backup
// storage config. Only s3 configs carry a secret. It is hoisted OUT of the
// config JSONB into the encrypted secret_enc column at rest and re-injected
// only on the provider-build read path. Must match the wire field name the
// handler and node use (handlers.backupStorageSecretField).
const backupStorageSecretField = "secretAccessKey"

// errNoBackupStorageKey is returned when a secret would have to be encrypted but
// no key is configured. It fails the write closed rather than persisting a
// plaintext secret.
var errNoBackupStorageKey = errors.New("backup storages: no encryption key configured; refusing to store a plaintext secret")

// SetBackupStorageEncryptionKey installs the at-rest key, derived from
// CLUSTER_SECRET. Called once at boot. Until it is set (tests, early boot) a
// write carrying a non-empty secret fails closed and a read of an encrypted
// secret fails to empty.
func (s *PostgresStore) SetBackupStorageEncryptionKey(clusterSecret string) {
	if clusterSecret == "" {
		return
	}
	s.backupStorageSecretKey = crypto.DeriveKey(clusterSecret, backupStorageSecretPurpose)
}

// encodeBackupStorageSecret encrypts a backup secret for storage. An empty
// secret stores an empty column (no secret, so non-s3 providers never need a
// key). It fails CLOSED: with no key configured it refuses to persist a
// plaintext secret.
func (s *PostgresStore) encodeBackupStorageSecret(secret string) (string, error) {
	if secret == "" {
		return "", nil
	}
	if s.backupStorageSecretKey == nil {
		return "", errNoBackupStorageKey
	}
	return crypto.Encrypt(s.backupStorageSecretKey, []byte(secret))
}

// decodeBackupStorageSecret decrypts a stored secret on read. An empty column is
// no secret. An encrypted value with no key configured, or one that fails to
// decrypt (a rotated CLUSTER_SECRET), returns "" and logs - a provider build
// then fails cleanly instead of authenticating with ciphertext-as-secret.
func (s *PostgresStore) decodeBackupStorageSecret(enc string) string {
	if enc == "" {
		return ""
	}
	if s.backupStorageSecretKey == nil {
		log.Printf("backup storages: a secret is encrypted but no encryption key is configured")
		return ""
	}
	pt, err := crypto.Decrypt(s.backupStorageSecretKey, enc)
	if err != nil {
		log.Printf("backup storages: could not decrypt a secret: %v", err)
		return ""
	}
	return string(pt)
}

// splitBackupStorageSecret separates the s3 secret from the rest of the config
// for the WRITE path: it returns the config with the secret field REMOVED and
// the secret string pulled out, so the caller stores clean config plus an
// encrypted secret_enc. A non-s3 provider, an empty config, or an unparseable
// config yields the config unchanged and an empty secret.
func splitBackupStorageSecret(provider string, config json.RawMessage) (json.RawMessage, string) {
	if provider != "s3" || len(config) == 0 {
		return config, ""
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(config, &m); err != nil {
		return config, ""
	}
	secret := jsonRawString(m[backupStorageSecretField])
	if _, ok := m[backupStorageSecretField]; !ok {
		return config, ""
	}
	delete(m, backupStorageSecretField)
	out, err := json.Marshal(m)
	if err != nil {
		return config, secret
	}
	return out, secret
}

// injectBackupStorageSecret puts the secret BACK into the config for the
// provider-build read path (factory.Open and the node transport both read the
// secret from config). A non-s3 provider or an empty secret returns the config
// unchanged. An unparseable config is returned unchanged.
func injectBackupStorageSecret(provider string, config json.RawMessage, secret string) json.RawMessage {
	if provider != "s3" || secret == "" {
		return config
	}
	m := map[string]json.RawMessage{}
	if len(config) > 0 {
		if err := json.Unmarshal(config, &m); err != nil {
			return config
		}
	}
	raw, err := json.Marshal(secret)
	if err != nil {
		return config
	}
	m[backupStorageSecretField] = raw
	out, err := json.Marshal(m)
	if err != nil {
		return config
	}
	return out
}

// stripBackupStorageSecret removes the s3 secret from the config for the LIST
// (panel) path and reports whether one was present. A legacy row keeps its
// secret plaintext in config until its next write, so this strip is what keeps
// a list view from leaking it even before migration. A non-s3 provider or empty
// config returns unchanged with false.
func stripBackupStorageSecret(provider string, config json.RawMessage) (json.RawMessage, bool) {
	if provider != "s3" || len(config) == 0 {
		return config, false
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(config, &m); err != nil {
		// An unreadable config cannot be safely redacted field-by-field, so
		// drop the whole blob rather than risk leaking part of it.
		return json.RawMessage("{}"), false
	}
	had := jsonRawString(m[backupStorageSecretField]) != ""
	delete(m, backupStorageSecretField)
	out, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage("{}"), had
	}
	return out, had
}

// jsonRawString decodes a JSON value into a string, returning "" on anything
// that is not a non-empty string.
func jsonRawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}
