package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestDeriveLANCertDeterministic(t *testing.T) {
	c1, fp1, err := DeriveLANCert("secret-abc", "node-1")
	if err != nil {
		t.Fatal(err)
	}
	c2, fp2, err := DeriveLANCert("secret-abc", "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Fatalf("fingerprint not deterministic: %s vs %s", fp1, fp2)
	}
	// The fingerprint must equal SHA-256 of the served leaf DER (what the app pins).
	leaf := sha256.Sum256(c1.Certificate[0])
	if hex.EncodeToString(leaf[:]) != fp1 {
		t.Fatalf("fingerprint does not match leaf DER")
	}
	_ = c2

	// Different node or secret => different fingerprint.
	_, fpOtherNode, _ := DeriveLANCert("secret-abc", "node-2")
	if fpOtherNode == fp1 {
		t.Fatal("expected different fingerprint for different node")
	}
	_, fpOtherSecret, _ := DeriveLANCert("secret-xyz", "node-1")
	if fpOtherSecret == fp1 {
		t.Fatal("expected different fingerprint for different secret")
	}

	if len(c1.Certificate) == 0 || c1.PrivateKey == nil {
		t.Fatal("cert incomplete")
	}
}
