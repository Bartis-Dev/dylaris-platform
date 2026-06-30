package redisacl

import (
	"bytes"
	"testing"
)

// memSecretStore is a tiny in-memory secretStore for round-trip testing.
type memSecretStore struct {
	enc map[int]string
}

func newMemSecretStore() *memSecretStore {
	return &memSecretStore{enc: make(map[int]string)}
}

func (m *memSecretStore) GetNodeSecretEnc(id int) (string, error) {
	return m.enc[id], nil
}

func (m *memSecretStore) SetNodeSecretEnc(id int, enc string) error {
	m.enc[id] = enc
	return nil
}

func TestLoadOrCreateNodeSecret_MintThenLoad(t *testing.T) {
	st := newMemSecretStore()
	const cluster = "test-cluster-secret"

	first, err := LoadOrCreateNodeSecret(st, cluster, 7)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("expected 32-byte secret, got %d", len(first))
	}
	if st.enc[7] == "" {
		t.Fatal("expected ciphertext to be persisted")
	}

	// Second call must decrypt the stored ciphertext and return the same secret.
	second, err := LoadOrCreateNodeSecret(st, cluster, 7)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("mint-then-load returned a different secret")
	}
}

func TestLoadOrCreateNodeSecret_RemintsOnCorruption(t *testing.T) {
	st := newMemSecretStore()
	const cluster = "test-cluster-secret"

	if _, err := LoadOrCreateNodeSecret(st, cluster, 1); err != nil {
		t.Fatalf("mint: %v", err)
	}
	// Corrupt the stored ciphertext; next call must self-heal by re-minting.
	st.enc[1] = "not-valid-hex-ciphertext"

	got, err := LoadOrCreateNodeSecret(st, cluster, 1)
	if err != nil {
		t.Fatalf("re-mint: %v", err)
	}
	if len(got) != 32 {
		t.Fatalf("expected 32-byte secret after re-mint, got %d", len(got))
	}
	if st.enc[1] == "not-valid-hex-ciphertext" {
		t.Fatal("expected corrupted ciphertext to be replaced")
	}
}
