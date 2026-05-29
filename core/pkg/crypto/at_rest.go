// Package crypto provides at-rest encryption for small platform secrets
// (Phase 14: Modrinth PAT; future: SMTP password, S3 keys, etc.).
//
// AES-256-GCM with a 32-byte key derived from the platform's CLUSTER_SECRET
// + a per-purpose salt. Ciphertext is hex-encoded with a 12-byte nonce
// prefix so a single column can hold the full encrypted payload without a
// separate nonce field.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

const nonceSize = 12

// DeriveKey produces a 32-byte AES key from a base secret + a purpose-tag.
// The tag scopes keys so the same secret can encrypt unrelated buckets
// (e.g. "modrinth-pat" vs future "smtp-password") without one's leak
// compromising the other.
func DeriveKey(baseSecret, purpose string) []byte {
	h := sha256.New()
	h.Write([]byte(baseSecret))
	h.Write([]byte{0x1f}) // separator byte
	h.Write([]byte(purpose))
	return h.Sum(nil)
}

// Encrypt seals plaintext with the supplied key and returns a hex string
// containing nonce|ciphertext. The output is safe to store in a TEXT column.
func Encrypt(key []byte, plaintext []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("crypto: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, nonceSize+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return hex.EncodeToString(out), nil
}

// Decrypt reverses Encrypt. Returns an error on tampered ciphertext.
func Decrypt(key []byte, encoded string) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("crypto: key must be 32 bytes")
	}
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(raw) < nonceSize {
		return nil, errors.New("crypto: ciphertext too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := raw[:nonceSize]
	ct := raw[nonceSize:]
	return gcm.Open(nil, nonce, ct, nil)
}
