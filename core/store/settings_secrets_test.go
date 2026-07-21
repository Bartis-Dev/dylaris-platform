package store

import (
	"strings"
	"testing"
)

func keyedStore() *PostgresStore {
	s := NewPostgresStore(nil) // encode/decode never touch the DB
	s.SetSettingsEncryptionKey("a-strong-cluster-secret")
	return s
}

const secretKey = "core_storage_s3_secret_key"

// TestSettingSecret_RoundTrips is the core: a secret setting encrypts on the way
// in and decrypts on the way out, and the stored form never contains the
// plaintext.
func TestSettingSecret_RoundTrips(t *testing.T) {
	s := keyedStore()
	const secret = "AKIA-super-secret-value"

	stored, err := s.encodeSettingValue(secretKey, secret)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.HasPrefix(stored, settingsEncMarker) {
		t.Errorf("stored value has no encryption marker: %q", stored)
	}
	if strings.Contains(stored, secret) {
		t.Fatalf("stored value contains the plaintext secret: %q", stored)
	}
	if got := s.decodeSettingValue(secretKey, stored); got != secret {
		t.Fatalf("decode = %q, want the original secret", got)
	}
}

// TestSettingSecret_LegacyPlaintextReadsThrough is what makes the migration
// lossless: a value written before encryption existed has no marker and must be
// returned as-is, even with a key configured.
func TestSettingSecret_LegacyPlaintextReadsThrough(t *testing.T) {
	s := keyedStore()
	if got := s.decodeSettingValue(secretKey, "old-plaintext-secret"); got != "old-plaintext-secret" {
		t.Fatalf("decode of a legacy plaintext = %q, want it unchanged", got)
	}
}

// TestSettingSecret_NonSecretKeyUntouched: only the credential keys are
// encrypted; an ordinary setting passes through both ways.
func TestSettingSecret_NonSecretKeyUntouched(t *testing.T) {
	s := keyedStore()
	stored, err := s.encodeSettingValue("frontend_url", "https://panel.example")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if stored != "https://panel.example" {
		t.Errorf("a non-secret setting was altered: %q", stored)
	}
	if got := s.decodeSettingValue("frontend_url", stored); got != "https://panel.example" {
		t.Errorf("decode of a non-secret setting = %q", got)
	}
}

// TestSettingSecret_EmptyStaysEmpty: clearing a secret (e.g. switching off s3)
// must store an empty string, not an encrypted blob.
func TestSettingSecret_EmptyStaysEmpty(t *testing.T) {
	s := keyedStore()
	stored, err := s.encodeSettingValue(secretKey, "")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if stored != "" {
		t.Errorf("an empty secret encoded to %q, want empty", stored)
	}
}

// TestSettingSecret_NoKeyPassesThrough: before the key is installed (tests,
// early boot), the secret settings behave exactly as before encryption existed.
func TestSettingSecret_NoKeyPassesThrough(t *testing.T) {
	s := NewPostgresStore(nil) // no key set

	stored, err := s.encodeSettingValue(secretKey, "plain")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if stored != "plain" {
		t.Errorf("encode without a key changed the value: %q", stored)
	}
	if got := s.decodeSettingValue(secretKey, "plain"); got != "plain" {
		t.Errorf("decode without a key changed the value: %q", got)
	}
}

// TestSettingSecret_EncryptedButNoKeyReturnsEmpty: an encrypted value that
// cannot be decrypted (key missing) returns "" so a provider build fails
// cleanly rather than treating ciphertext as a secret.
func TestSettingSecret_EncryptedButNoKeyReturnsEmpty(t *testing.T) {
	enc, err := keyedStore().encodeSettingValue(secretKey, "secret")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	nokey := NewPostgresStore(nil)
	if got := nokey.decodeSettingValue(secretKey, enc); got != "" {
		t.Fatalf("decode of an encrypted value with no key = %q, want empty", got)
	}
}

// TestSettingSecret_WrongKeyReturnsEmpty: a rotated CLUSTER_SECRET cannot read
// the old ciphertext; it must fail closed to empty, not leak or crash.
func TestSettingSecret_WrongKeyReturnsEmpty(t *testing.T) {
	enc, err := keyedStore().encodeSettingValue(secretKey, "secret")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	other := NewPostgresStore(nil)
	other.SetSettingsEncryptionKey("a-different-cluster-secret")
	if got := other.decodeSettingValue(secretKey, enc); got != "" {
		t.Fatalf("decode with the wrong key = %q, want empty", got)
	}
}
