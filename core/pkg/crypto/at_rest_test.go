package crypto

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// testKey derives a deterministic 32-byte key the same way real callers do
// (fixed base secret + a purpose tag, e.g. handlers.modrinth_pat).
func testKey(t *testing.T, purpose string) []byte {
	t.Helper()
	k := DeriveKey("cluster-secret-under-test", purpose)
	if len(k) != 32 {
		t.Fatalf("DeriveKey returned %d bytes, want 32", len(k))
	}
	return k
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := testKey(t, "round-trip")

	cases := []struct {
		name      string
		plaintext []byte
	}{
		{"empty", []byte{}},
		{"nil", nil},
		{"short", []byte("mrp_abc123DEF456")},
		{"binary", []byte{0x00, 0xff, 0x01, 0xfe, 0x00, 0x00, 0x80}},
		{"multi-KB", bytes.Repeat([]byte("dylaris-at-rest-secret-blob-"), 4096)}, // ~112 KB
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := Encrypt(key, tc.plaintext)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			// Output must be valid hex (safe for a TEXT column).
			if _, err := hex.DecodeString(enc); err != nil {
				t.Fatalf("ciphertext is not valid hex: %v", err)
			}
			got, err := Decrypt(key, enc)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(got, tc.plaintext) {
				t.Fatalf("round-trip mismatch: got %q, want %q", got, tc.plaintext)
			}
		})
	}
}

func TestEncrypt_NonceIsRandom(t *testing.T) {
	key := testKey(t, "nonce-random")
	plaintext := []byte("same-plaintext-same-key")

	a, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt a: %v", err)
	}
	b, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt b: %v", err)
	}
	if a == b {
		t.Fatal("two encryptions of the same plaintext+key produced identical ciphertext (nonce not random)")
	}

	// Both must still decrypt back to the original.
	for i, enc := range []string{a, b} {
		got, err := Decrypt(key, enc)
		if err != nil {
			t.Fatalf("Decrypt #%d: %v", i, err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Fatalf("Decrypt #%d mismatch: got %q, want %q", i, got, plaintext)
		}
	}
}

// TestDecrypt_TamperDetection flips every single byte of a valid ciphertext in
// turn (nonce, ciphertext body, and GCM tag) and asserts each corruption is
// rejected. GCM must never return silent garbage.
func TestDecrypt_TamperDetection(t *testing.T) {
	key := testKey(t, "tamper")
	plaintext := []byte("modrinth-personal-access-token-payload")

	enc, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	raw, err := hex.DecodeString(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i := 0; i < len(raw); i++ {
		tampered := make([]byte, len(raw))
		copy(tampered, raw)
		tampered[i] ^= 0xff
		got, err := Decrypt(key, hex.EncodeToString(tampered))
		if err == nil {
			t.Fatalf("byte %d flipped: expected auth error, got plaintext %q", i, got)
		}
		if got != nil {
			t.Fatalf("byte %d flipped: expected nil plaintext on error, got %q", i, got)
		}
	}
}

// TestDecrypt_ShortOrInvalid covers inputs that are too short to hold a nonce +
// GCM tag, plus a non-hex string. None may panic; all must return an error.
func TestDecrypt_ShortOrInvalid(t *testing.T) {
	key := testKey(t, "short")

	cases := []struct {
		name    string
		encoded string
	}{
		{"empty", ""},
		{"one-byte", hex.EncodeToString([]byte{0x01})},
		{"eleven-bytes", hex.EncodeToString(bytes.Repeat([]byte{0x02}, 11))},
		{"nonce-only-no-tag", hex.EncodeToString(bytes.Repeat([]byte{0x03}, nonceSize))},
		{"nonce-plus-short-tag", hex.EncodeToString(bytes.Repeat([]byte{0x04}, nonceSize+15))},
		{"not-hex", "zz-not-hex-zz"},
		{"odd-length-hex", "abc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decrypt(key, tc.encoded)
			if err == nil {
				t.Fatalf("expected error, got plaintext %q", got)
			}
			if got != nil {
				t.Fatalf("expected nil plaintext on error, got %q", got)
			}
		})
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	keyA := testKey(t, "wrong-key-a")
	keyB := DeriveKey("a-completely-different-cluster-secret", "wrong-key-a")
	if bytes.Equal(keyA, keyB) {
		t.Fatal("test setup: the two keys must differ")
	}

	enc, err := Encrypt(keyA, []byte("secret-under-key-a"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := Decrypt(keyB, enc)
	if err == nil {
		t.Fatalf("expected error decrypting with the wrong key, got plaintext %q", got)
	}
}

// TestKeySizeGuard verifies both Encrypt and Decrypt reject keys that are not
// exactly 32 bytes rather than silently misbehaving.
func TestKeySizeGuard(t *testing.T) {
	validKey := testKey(t, "size-guard")
	valid, err := Encrypt(validKey, []byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt with valid key: %v", err)
	}

	badKeys := map[string][]byte{
		"empty":      {},
		"too-short":  bytes.Repeat([]byte{0x01}, 16),
		"too-long":   bytes.Repeat([]byte{0x01}, 33),
		"aes192-ish": bytes.Repeat([]byte{0x01}, 24),
	}
	for name, bad := range badKeys {
		t.Run("encrypt/"+name, func(t *testing.T) {
			if _, err := Encrypt(bad, []byte("payload")); err == nil {
				t.Fatal("expected error for non-32-byte key")
			}
		})
		t.Run("decrypt/"+name, func(t *testing.T) {
			if _, err := Decrypt(bad, valid); err == nil {
				t.Fatal("expected error for non-32-byte key")
			}
		})
	}
}

func TestDeriveKey_LengthAndDeterminism(t *testing.T) {
	k1 := DeriveKey("secret", "modrinth-pat")
	k2 := DeriveKey("secret", "modrinth-pat")
	if len(k1) != 32 {
		t.Fatalf("DeriveKey length: got %d, want 32", len(k1))
	}
	if !bytes.Equal(k1, k2) {
		t.Fatal("DeriveKey is not deterministic: same secret+purpose gave different keys")
	}
}

func TestDeriveKey_PurposeAndSecretScoping(t *testing.T) {
	base := DeriveKey("cluster-secret", "modrinth-pat")

	// Different purpose, same secret -> different key.
	if bytes.Equal(base, DeriveKey("cluster-secret", "node-redis-secret")) {
		t.Fatal("different purpose tags produced the same key")
	}
	// Different secret, same purpose -> different key.
	if bytes.Equal(base, DeriveKey("other-cluster-secret", "modrinth-pat")) {
		t.Fatal("different base secrets produced the same key")
	}
}

// TestDeriveKey_PurposeScopedCiphertext is the end-to-end guarantee callers
// rely on: a payload sealed under purpose A cannot be opened under purpose B,
// while the same purpose round-trips.
func TestDeriveKey_PurposeScopedCiphertext(t *testing.T) {
	const secret = "cluster-secret"
	keyA := DeriveKey(secret, "modrinth-pat")
	keyB := DeriveKey(secret, "smtp-password")

	plaintext := []byte("mrp_topsecret")
	enc, err := Encrypt(keyA, plaintext)
	if err != nil {
		t.Fatalf("Encrypt under purpose A: %v", err)
	}

	// Same purpose round-trips.
	got, err := Decrypt(DeriveKey(secret, "modrinth-pat"), enc)
	if err != nil {
		t.Fatalf("Decrypt under same purpose: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("same-purpose round-trip mismatch: got %q, want %q", got, plaintext)
	}

	// Cross-purpose must fail.
	if out, err := Decrypt(keyB, enc); err == nil {
		t.Fatalf("expected cross-purpose decrypt to fail, got plaintext %q", out)
	}
}
