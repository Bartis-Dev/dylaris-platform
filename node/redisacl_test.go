package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestACLGoldenVectors MUST match core/services/redisacl/derive_test.go's
// TestGoldenVectors exactly. secret = "0123456789abcdef0123456789abcdef", token = "node-a".
func TestACLGoldenVectors(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	const token = "node-a"
	cases := []struct{ name, got, want string }{
		{"node", aclNodePassword(secret, token), "713d2c1b3d181aaee59c162fccc84610e695262c79d2bfb3d738991e0aef8487"},
		{"shipper", aclShipperPassword(secret, token), "a2fd4a4c4ae6cee28a0d72f89620f2c77680c075041706e5f61c9213e3201096"},
		{"proof", aclProof(secret, token), "dbed444099043c96ff66b685e59a489f532fb41387bcdba74e5b2e382f5f9ec9"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s vector drift vs Core:\n got  %s\n want %s", c.name, c.got, c.want)
		}
	}
	if aclNodeUsername("x") != "node-x" || aclShipperUsername("x") != "node-x-shipper" {
		t.Fatal("username format must match Core")
	}
}

func TestNodeSecretRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := loadNodeSecret(dir); ok {
		t.Fatal("expected no secret in empty dir")
	}
	secret := []byte("0123456789abcdef0123456789abcdef")
	if err := saveNodeSecret(dir, secret); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := loadNodeSecret(dir)
	if !ok || string(got) != string(secret) {
		t.Fatalf("round-trip mismatch: ok=%v got=%q", ok, got)
	}
	// malformed file -> ok=false
	_ = os.WriteFile(filepath.Join(dir, ".node_secret"), []byte("nothex"), 0600)
	if _, ok := loadNodeSecret(dir); ok {
		t.Fatal("malformed secret must yield ok=false")
	}
}
