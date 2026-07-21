package handlers

import (
	"encoding/json"

	"dylaris-core/models"
)

// backupStorageSecretField is the one credential field inside an s3 backup
// storage config. Only s3 configs carry a secret; every other provider's config
// is non-sensitive.
const backupStorageSecretField = "secretAccessKey"

// redactBackupStorageSecret returns a copy of the storage with its s3 secret
// removed from Config and SecretSet reflecting whether one was stored. Used on
// every read path so the secret never leaves Core. A non-s3 provider, or an
// unparseable config, is returned unchanged with SecretSet false.
func redactBackupStorageSecret(bs models.BackupStorage) models.BackupStorage {
	if bs.Provider != "s3" || len(bs.Config) == 0 {
		return bs
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(bs.Config, &m); err != nil {
		// An unreadable config cannot be safely redacted field-by-field, so
		// drop the whole blob rather than risk leaking part of it.
		bs.Config = json.RawMessage("{}")
		return bs
	}
	if raw, ok := m[backupStorageSecretField]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			bs.SecretSet = true
		}
		delete(m, backupStorageSecretField)
	}
	if out, err := json.Marshal(m); err == nil {
		bs.Config = out
	} else {
		bs.Config = json.RawMessage("{}")
	}
	return bs
}

// mergeBackupStorageSecret backfills the s3 secret from the stored config when
// the incoming config omits it or sends it blank. This is what lets the panel
// present the secret as write-only ("leave blank to keep") without an edit
// wiping it: the form never receives the secret, so a save carries none, and
// the existing one is preserved unless a new value is supplied.
//
// A non-blank incoming secret is used as-is (a genuine rotation). A non-s3
// provider is returned unchanged.
func mergeBackupStorageSecret(incoming models.BackupStorage, existing *models.BackupStorage) models.BackupStorage {
	if incoming.Provider != "s3" || existing == nil {
		return incoming
	}
	in := map[string]json.RawMessage{}
	if len(incoming.Config) > 0 {
		if err := json.Unmarshal(incoming.Config, &in); err != nil {
			return incoming
		}
	}
	if s := jsonString(in[backupStorageSecretField]); s != "" {
		return incoming // a new secret was submitted; keep it
	}
	ex := map[string]json.RawMessage{}
	if err := json.Unmarshal(existing.Config, &ex); err != nil {
		return incoming
	}
	if raw, ok := ex[backupStorageSecretField]; ok {
		in[backupStorageSecretField] = raw
		if out, err := json.Marshal(in); err == nil {
			incoming.Config = out
		}
	}
	return incoming
}

// jsonString decodes a JSON value into a string, returning "" on anything that
// is not a non-empty string.
func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}
