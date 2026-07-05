// Package migration is the transport primitive for the auto-move feature:
// pulling a zipped server directory from one node to another over a tiny
// dedicated HTTP endpoint. It is shared by core (mints pull tokens) and node
// (serves the archive + pulls it). Wave 2a is transport only — no command
// handlers, queue wiring, or orchestrator live here.
package migration

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// tokenPurpose scopes the HMAC key so a CLUSTER_SECRET leak in one subsystem
// can't be replayed as a migration token (mirrors crypto.DeriveKey's tag idea).
const tokenPurpose = "dylaris/migration/v1"

var (
	// ErrTokenInvalid covers any structural/signature failure. We deliberately
	// don't distinguish malformed-vs-bad-signature to avoid handing an attacker
	// an oracle for which part of a forged token was wrong.
	ErrTokenInvalid = errors.New("migration: token invalid")
	// ErrTokenExpired is returned only after the signature has been verified —
	// so an expired-but-authentic token is distinguishable from a forgery.
	ErrTokenExpired = errors.New("migration: token expired")
)

// Claims is the migration pull authorization payload.
type Claims struct {
	ServerUUID   string `json:"server_uuid"`
	SourceNodeID string `json:"source_node_id"`
	ExpiresAt    int64  `json:"expires_at"` // unix seconds
}

// deriveKey produces the 32-byte HMAC key from the caller-supplied secret +
// purpose. Same construction as core/pkg/crypto.DeriveKey (sha256 of
// secret|sep|purpose), duplicated here to keep the shared module
// dependency-free of core.
func deriveKey(secret string) []byte {
	h := sha256.New()
	h.Write([]byte(secret))
	h.Write([]byte{0x1f}) // separator byte
	h.Write([]byte(tokenPurpose))
	return h.Sum(nil)
}

// MintToken produces a one-time pull token: base64url(JSON claims) + "." +
// base64url(HMAC-SHA256(key, payload)). TTL sets the ExpiresAt claim.
func MintToken(secret, serverUUID, sourceNodeID string, ttl time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("migration: empty cluster secret")
	}
	c := Claims{
		ServerUUID:   serverUUID,
		SourceNodeID: sourceNodeID,
		ExpiresAt:    time.Now().Add(ttl).Unix(),
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	sig := sign(deriveKey(secret), payload)
	return payload + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifyToken checks the HMAC in constant time, then enforces expiry. A bad
// signature returns ErrTokenInvalid; an authentic-but-stale token returns
// ErrTokenExpired.
func VerifyToken(secret, token string) (Claims, error) {
	if secret == "" {
		return Claims{}, errors.New("migration: empty cluster secret")
	}
	dot := -1
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		return Claims{}, ErrTokenInvalid
	}
	payload := token[:dot]
	sigB64 := token[dot+1:]

	gotSig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return Claims{}, ErrTokenInvalid
	}
	wantSig := sign(deriveKey(secret), payload)
	if !hmac.Equal(gotSig, wantSig) {
		return Claims{}, ErrTokenInvalid
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Claims{}, ErrTokenInvalid
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return Claims{}, ErrTokenInvalid
	}
	// Expiry is checked only after a successful HMAC compare so the error
	// distinguishes a genuine-but-stale token from a forgery.
	if time.Now().Unix() >= c.ExpiresAt {
		return Claims{}, ErrTokenExpired
	}
	return c, nil
}

func sign(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}
