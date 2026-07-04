// Package redisacl provides deterministic per-node Redis (Valkey) credential
// derivation and ACL-rule construction for BYON node isolation.
//
// The derive functions MUST be reproduced byte-identically by the node agent
// (node/redisacl.go, a separate Go module). No cross-module test can enforce
// that directly, so TestGoldenVectors below pins the exact wire format: the
// node side must produce the same hex for the same (secret, token). A drift of
// a single character in either copy silently breaks every node's Redis auth.
package redisacl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// NodeUsername / ShipperUsername are the ACL usernames for a node.
func NodeUsername(token string) string    { return "node-" + token }
func ShipperUsername(token string) string { return "node-" + token + "-shipper" }

// NodePassword derives the node agent's Redis password from its per-node secret.
func NodePassword(secret []byte, token string) string {
	return derive(secret, "dylaris-redis-acl:v1:node:"+token)
}

// ShipperPassword derives the MC-container (log-shipper) Redis password.
func ShipperPassword(secret []byte, token string) string {
	return derive(secret, "dylaris-redis-acl:v1:shipper:"+token)
}

// Proof is the HMAC the node presents to prove it holds the per-node secret.
func Proof(secret []byte, token string) string {
	return derive(secret, "dylaris-redis-acl:v1:proof:"+token)
}

// VerifyProof constant-time compares a presented proof against the expected one.
func VerifyProof(secret []byte, token, got string) bool {
	return hmac.Equal([]byte(Proof(secret, token)), []byte(got))
}

// ChallengeResponse is the HMAC the node returns for a Core-issued nonce, proving
// possession of the per-node secret without a replayable static value.
func ChallengeResponse(secret []byte, nonce string) string {
	return derive(secret, "dylaris-redis-acl:v1:challenge:"+nonce)
}

// VerifyChallenge constant-time compares a presented challenge response against
// the expected one for the given nonce.
func VerifyChallenge(secret []byte, nonce, got string) bool {
	return hmac.Equal([]byte(ChallengeResponse(secret, nonce)), []byte(got))
}

// ClusterProof is the HMAC a node presents to prove it holds CLUSTER_SECRET,
// used to gate first-issuance of a known node's per-node secret. Keyed by
// CLUSTER_SECRET (NOT the per-node secret), so a bare-token attacker without
// CLUSTER_SECRET cannot forge it.
func ClusterProof(clusterSecret, token string) string {
	m := hmac.New(sha256.New, []byte(clusterSecret))
	m.Write([]byte("dylaris-node-cluster-proof:v1:" + token))
	return hex.EncodeToString(m.Sum(nil))
}

func derive(secret []byte, domain string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(domain))
	return hex.EncodeToString(m.Sum(nil))
}
