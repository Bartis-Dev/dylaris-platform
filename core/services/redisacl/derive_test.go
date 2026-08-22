package redisacl

import "testing"

func TestDeriveDeterministicAndDistinct(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	p1 := NodePassword(secret, "node-a")
	p2 := NodePassword(secret, "node-a")
	if p1 != p2 {
		t.Fatal("NodePassword not deterministic")
	}
	if len(p1) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(p1))
	}
	if NodePassword(secret, "node-b") == p1 {
		t.Fatal("different tokens must yield different passwords")
	}
	if ShipperPassword(secret, "node-a", "srv-1") == p1 {
		t.Fatal("node vs shipper passwords must differ")
	}
	// Per SERVER: two containers on one node must not derive the same credential,
	// or splitting the ACL user would buy nothing.
	if ShipperPassword(secret, "node-a", "srv-1") == ShipperPassword(secret, "node-a", "srv-2") {
		t.Fatal("two servers on the same node derive the same shipper password")
	}
	if NodeUsername("x") != "node-x" || ShipperUsername("x", "s1") != "node-x-shipper-s1" {
		t.Fatal("username format wrong")
	}
}

// TestGoldenVectors pins the exact wire format so the node-side duplicate
// (node/redisacl.go) can assert against the SAME known-answer values. If this
// test changes, the node side MUST change in lockstep, and every already-enrolled
// node's derived password changes too. secret = "0123456789abcdef0123456789abcdef",
// token = "node-a".
func TestGoldenVectors(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	const token = "node-a"
	// The shipper credential is per SERVER, so the vector needs a server uuid too.
	const vectorServer = "srv-1"
	cases := []struct {
		name, got, want string
	}{
		{"node", NodePassword(secret, token), "713d2c1b3d181aaee59c162fccc84610e695262c79d2bfb3d738991e0aef8487"},
		{"shipper", ShipperPassword(secret, token, vectorServer), "16b3871d0705f9100a19d454d4d6c3b8b61d23b75c60206cc5c377f7d231cf55"},
		{"proof", Proof(secret, token), "dbed444099043c96ff66b685e59a489f532fb41387bcdba74e5b2e382f5f9ec9"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s vector drift:\n got  %s\n want %s", c.name, c.got, c.want)
		}
	}
}

func TestProofRoundTrip(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	p := Proof(secret, "node-a")
	if !VerifyProof(secret, "node-a", p) {
		t.Fatal("valid proof must verify")
	}
	if VerifyProof(secret, "node-a", "deadbeef") {
		t.Fatal("bad proof must not verify")
	}
	if VerifyProof([]byte("different-secret-padding-32bytes!"), "node-a", p) {
		t.Fatal("proof must not verify under a different secret")
	}
}
