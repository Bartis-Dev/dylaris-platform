package store

import (
	"log"
	"strings"

	"dylaris-core/pkg/crypto"
)

// settingsSecretKeys are the settings whose value is a credential that must be
// encrypted at rest. GetSetting decrypts them transparently and SetSetting
// encrypts them, so every caller keeps seeing plaintext while the DB never
// holds the secret in the clear. The backup s3 secret is NOT here - it lives in
// backup_storages.config JSONB and is handled separately.
var settingsSecretKeys = map[string]bool{
	"core_storage_s3_secret_key":    true,
	"modpack_storage_s3_secret_key": true,
}

// settingsEncMarker prefixes an encrypted value so a read can tell it apart from
// a legacy plaintext value written before encryption existed. Migration is
// therefore lazy and lossless: an old plaintext secret reads through unchanged
// and is re-encrypted the next time it is saved.
const settingsEncMarker = "enc:v1:"

const settingsSecretPurpose = "storage-settings-secret"

// SetSettingsEncryptionKey installs the at-rest key, derived from CLUSTER_SECRET.
// Called once at boot. Until it is set - tests, early boot - the secret
// settings pass through in the clear, which is the pre-encryption behaviour, so
// nothing breaks if it is never called.
func (s *PostgresStore) SetSettingsEncryptionKey(clusterSecret string) {
	if clusterSecret == "" {
		return
	}
	s.settingsSecretKey = crypto.DeriveKey(clusterSecret, settingsSecretPurpose)
}

// encodeSettingValue encrypts a secret setting for storage. A non-secret key, an
// empty value, or a store with no key configured passes through unchanged. It
// fails closed: an encryption error is returned rather than persisting the
// plaintext, which would be the exact leak this exists to prevent.
func (s *PostgresStore) encodeSettingValue(key, value string) (string, error) {
	if value == "" || s.settingsSecretKey == nil || !settingsSecretKeys[key] {
		return value, nil
	}
	enc, err := crypto.Encrypt(s.settingsSecretKey, []byte(value))
	if err != nil {
		return "", err
	}
	return settingsEncMarker + enc, nil
}

// decodeSettingValue decrypts a secret setting on read. A non-secret key, or a
// value without the marker (legacy plaintext), passes through. An encrypted
// value with no key configured, or one that fails to decrypt, returns "" - a
// provider build then fails cleanly instead of using ciphertext as a secret.
func (s *PostgresStore) decodeSettingValue(key, value string) string {
	if !settingsSecretKeys[key] || !strings.HasPrefix(value, settingsEncMarker) {
		return value
	}
	if s.settingsSecretKey == nil {
		log.Printf("settings: %s is encrypted but no encryption key is configured", key)
		return ""
	}
	pt, err := crypto.Decrypt(s.settingsSecretKey, strings.TrimPrefix(value, settingsEncMarker))
	if err != nil {
		log.Printf("settings: could not decrypt %s: %v", key, err)
		return ""
	}
	return string(pt)
}
