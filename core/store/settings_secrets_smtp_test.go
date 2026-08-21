package store

import (
	"strings"
	"testing"

	"dylaris-core/pkg/crypto"
)

// The settings table encrypts the credentials it is told to encrypt, and the
// SMTP password was simply not on the list - the one credential in there stored
// in the clear, next to an S3 secret and a DNS token that both were. It is
// written through the same encode path, so only the key predicate decided it.

func TestIsSecretSettingKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"core_storage_s3_secret_key", true},
		{"modpack_storage_s3_secret_key", true},
		{"dns.api_token", true},
		{"smtp.default.password", true},
		{"smtp.tickets.password", true}, // the key is parameterised by purpose
		{"smtp.default.username", false},
		{"smtp.default.host", false},
		{"branding.name", false},
		// The prefix matters: a ".password" key outside the smtp namespace is
		// not silently swept in by the suffix alone.
		{"some.other.password", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isSecretSettingKey(c.key); got != c.want {
			t.Errorf("isSecretSettingKey(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}

func TestSMTPPasswordRoundTripsEncrypted(t *testing.T) {
	s := &PostgresStore{settingsSecretKey: crypto.DeriveKey("test-cluster-secret", settingsSecretPurpose)}
	const key = "smtp.default.password"
	const plain = "hunter2-but-longer"

	stored, err := s.encodeSettingValue(key, plain)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if stored == plain {
		t.Fatal("the SMTP password was stored verbatim")
	}
	if !strings.HasPrefix(stored, settingsEncMarker) {
		t.Fatalf("stored value carries no encryption marker: %q", stored)
	}
	if strings.Contains(stored, plain) {
		t.Fatal("the plaintext is still present inside the stored value")
	}
	if got := s.decodeSettingValue(key, stored); got != plain {
		t.Fatalf("decode = %q, want %q", got, plain)
	}
}

// A password saved before this change reads back unchanged (lazy migration),
// so mail keeps working until the operator next saves the form.
func TestLegacyPlaintextSMTPPasswordStillReads(t *testing.T) {
	s := &PostgresStore{settingsSecretKey: crypto.DeriveKey("test-cluster-secret", settingsSecretPurpose)}
	if got := s.decodeSettingValue("smtp.default.password", "old-plaintext"); got != "old-plaintext" {
		t.Fatalf("legacy value = %q, want it to read through unchanged", got)
	}
}

// A non-secret setting must not be touched, or every plain value in the table
// would start round-tripping through the cipher.
func TestNonSecretSettingIsUntouched(t *testing.T) {
	s := &PostgresStore{settingsSecretKey: crypto.DeriveKey("test-cluster-secret", settingsSecretPurpose)}
	stored, err := s.encodeSettingValue("smtp.default.host", "smtp.example.com")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if stored != "smtp.example.com" {
		t.Fatalf("a non-secret setting was transformed: %q", stored)
	}
}
