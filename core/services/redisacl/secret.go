package redisacl

import (
	"crypto/rand"

	"dylaris-core/pkg/crypto"
)

// secretStore is the subset of the Store this package needs.
type secretStore interface {
	GetNodeSecretEnc(id int) (string, error)
	SetNodeSecretEnc(id int, enc string) error
}

// LoadOrCreateNodeSecret returns the node's per-node secret (32 raw bytes),
// minting + persisting one (AES-256-GCM at rest) on first use. clusterSecret is
// the platform CLUSTER_SECRET; the at-rest key is derived with purpose tag
// "node-redis-secret".
func LoadOrCreateNodeSecret(st secretStore, clusterSecret string, nodeID int) ([]byte, error) {
	key := crypto.DeriveKey(clusterSecret, "node-redis-secret")
	enc, err := st.GetNodeSecretEnc(nodeID)
	if err == nil && enc != "" {
		if pt, derr := crypto.Decrypt(key, enc); derr == nil && len(pt) == 32 {
			return pt, nil
		}
		// fall through to re-mint on decrypt failure / corruption
	}
	secret := make([]byte, 32)
	if _, rerr := rand.Read(secret); rerr != nil {
		return nil, rerr
	}
	ct, eerr := crypto.Encrypt(key, secret)
	if eerr != nil {
		return nil, eerr
	}
	if serr := st.SetNodeSecretEnc(nodeID, ct); serr != nil {
		return nil, serr
	}
	return secret, nil
}

// LoadNodeSecret loads + decrypts an existing secret WITHOUT minting. ok=false
// when no secret is stored yet. Used for proof verification on reconnect.
func LoadNodeSecret(st secretStore, clusterSecret string, nodeID int) (secret []byte, ok bool, err error) {
	enc, gerr := st.GetNodeSecretEnc(nodeID)
	if gerr != nil {
		return nil, false, gerr
	}
	if enc == "" {
		return nil, false, nil
	}
	key := crypto.DeriveKey(clusterSecret, "node-redis-secret")
	pt, derr := crypto.Decrypt(key, enc)
	if derr != nil || len(pt) != 32 {
		return nil, false, derr
	}
	return pt, true, nil
}
