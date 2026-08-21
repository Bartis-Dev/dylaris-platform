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

// The node ID is public, so with an empty secret the seed - and the private key
// the LAN listener serves with - is derivable by anyone who knows the node's
// name. A node started without BEAM_JWT_SECRET reaches here: beam_server.go
// starts the LAN fast-path unconditionally, and the BYON deploy snippet hands
// out no fleet secrets on purpose. SignBeamTicket and ValidateBeamTicket in
// this same package already refuse an empty secret.
func TestDeriveLANCertRefusesAnEmptySecret(t *testing.T) {
	cert, fp, err := DeriveLANCert("", "node-1")
	if err == nil {
		t.Fatal("an empty secret produced a certificate; its private key is derivable from the public node ID alone")
	}
	if fp != "" || len(cert.Certificate) != 0 || cert.PrivateKey != nil {
		t.Fatalf("refused but still returned material: fp=%q cert=%+v", fp, cert)
	}
	// Core's fingerprint-only entry point must refuse it too, or it would hand
	// the app a pin for a certificate no honest node can serve.
	if _, err := LANCertFingerprint("", "node-1"); err == nil {
		t.Fatal("LANCertFingerprint accepted an empty secret")
	}
}
