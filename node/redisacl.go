package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// These derivation functions MUST stay byte-identical to the Core side
// (core/services/redisacl/derive.go). TestACLGoldenVectors pins the wire format;
// a drift here silently breaks this node's Redis auth.
func aclNodeUsername(token string) string    { return "node-" + token }
func aclShipperUsername(token string) string { return "node-" + token + "-shipper" }

func aclNodePassword(secret []byte, token string) string {
	return aclDerive(secret, "dylaris-redis-acl:v1:node:"+token)
}
func aclShipperPassword(secret []byte, token string) string {
	return aclDerive(secret, "dylaris-redis-acl:v1:shipper:"+token)
}
func aclProof(secret []byte, token string) string {
	return aclDerive(secret, "dylaris-redis-acl:v1:proof:"+token)
}

// aclClusterProof mirrors core redisacl.ClusterProof: HMAC(CLUSTER_SECRET, token).
func aclClusterProof(clusterSecret, token string) string {
	m := hmac.New(sha256.New, []byte(clusterSecret))
	m.Write([]byte("dylaris-node-cluster-proof:v1:" + token))
	return hex.EncodeToString(m.Sum(nil))
}

func aclDerive(secret []byte, domain string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(domain))
	return hex.EncodeToString(m.Sum(nil))
}

// loadNodeSecret reads the cached 32-byte secret (hex) from <workdir>/.node_secret.
// Returns ok=false when the file is missing or malformed.
func loadNodeSecret(workdir string) ([]byte, bool) {
	b, err := os.ReadFile(filepath.Join(workdir, ".node_secret"))
	if err != nil {
		return nil, false
	}
	raw, derr := hex.DecodeString(strings.TrimSpace(string(b)))
	if derr != nil || len(raw) != 32 {
		return nil, false
	}
	return raw, true
}

// saveNodeSecret persists the secret as hex with 0600 perms.
func saveNodeSecret(workdir string, secret []byte) error {
	return os.WriteFile(filepath.Join(workdir, ".node_secret"), []byte(hex.EncodeToString(secret)), 0600)
}
