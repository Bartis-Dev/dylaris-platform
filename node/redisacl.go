package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
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
func aclLinkUsername(token string) string { return "node-" + token + "-link" }
func aclLinkPassword(secret []byte, token string) string {
	return aclDerive(secret, "dylaris-redis-acl:v1:link:"+token)
}
func aclProof(secret []byte, token string) string {
	return aclDerive(secret, "dylaris-redis-acl:v1:proof:"+token)
}

// aclChallengeResponse mirrors core redisacl.ChallengeResponse: HMAC(secret,
// "dylaris-redis-acl:v1:challenge:"+nonce). Byte-identical to the Core side.
func aclChallengeResponse(secret []byte, nonce string) string {
	return aclDerive(secret, "dylaris-redis-acl:v1:challenge:"+nonce)
}

// aclHeartbeatSig mirrors core redisacl.HeartbeatSig: HMAC(perNodeSecret,
// "dylaris-redis-acl:v1:heartbeat:"+token+":"+ts). Byte-identical to Core.
func aclHeartbeatSig(secret []byte, token string, ts int64) string {
	return aclDerive(secret, "dylaris-redis-acl:v1:heartbeat:"+token+":"+strconv.FormatInt(ts, 10))
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

// loadNodeID reads the cached server-assigned node id from <workdir>/.node_id.
// Returns ok=false when the file is missing or empty.
func loadNodeID(workdir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(workdir, ".node_id"))
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", false
	}
	return id, true
}

// saveNodeID persists the server-assigned node id with 0600 perms.
func saveNodeID(workdir, id string) error {
	return os.WriteFile(filepath.Join(workdir, ".node_id"), []byte(id), 0600)
}

// loadLinkCreds reads the cached Core-delivered Link tunnel token + discovery proof
// from <workdir>/.link_secret and .link_discovery_proof. ok=false if either missing.
func loadLinkCreds(workdir string) (secret, proof string, ok bool) {
	s, err := os.ReadFile(filepath.Join(workdir, ".link_secret"))
	if err != nil {
		return "", "", false
	}
	p, err := os.ReadFile(filepath.Join(workdir, ".link_discovery_proof"))
	if err != nil {
		return "", "", false
	}
	secret = strings.TrimSpace(string(s))
	proof = strings.TrimSpace(string(p))
	if secret == "" || proof == "" {
		return "", "", false
	}
	return secret, proof, true
}

// saveLinkCreds persists the Link tunnel token + discovery proof (0600).
func saveLinkCreds(workdir, secret, proof string) error {
	if err := os.WriteFile(filepath.Join(workdir, ".link_secret"), []byte(secret), 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workdir, ".link_discovery_proof"), []byte(proof), 0600)
}
