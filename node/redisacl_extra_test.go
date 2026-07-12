package main

import "testing"

// TestACLGoldenVectorsExtended MUST match
// core/services/redisacl/derive_extra_test.go's TestGoldenVectorsExtended
// exactly (link/challenge/heartbeat/cluster-proof). RouteOnlyLinkPassword
// has no node-side equivalent (Core-only), so it is not cross-checked here
// - see the Core test's doc comment. This file only ADDS vectors; the
// original redisacl_test.go is untouched.
func TestACLGoldenVectorsExtended(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	const token = "node-a"

	if got, want := aclLinkPassword(secret, token), "6245236664161e1d08ad351374d45c986a98c6737f103c3479645936773fa3de"; got != want {
		t.Errorf("aclLinkPassword vector drift vs Core:\n got  %s\n want %s", got, want)
	}
	if got, want := aclLinkPassword(secret, "node-b"), "61980a19743ec48541a65d1b2d6b6f2a94b2370b90d6389ebd059b4795574415"; got != want {
		t.Errorf("aclLinkPassword(node-b) vector drift vs Core:\n got  %s\n want %s", got, want)
	}
	if aclLinkUsername("x") != "node-x-link" {
		t.Errorf("aclLinkUsername format must match Core: %s", aclLinkUsername("x"))
	}

	if got, want := aclChallengeResponse(secret, "test-nonce-1"), "d737f05576089d017677f16151a7b803c4adb9660a0bc6e2e045ec640414f34b"; got != want {
		t.Errorf("aclChallengeResponse vector drift vs Core:\n got  %s\n want %s", got, want)
	}

	if got, want := aclHeartbeatSig(secret, token, 1700000000), "f554fe0872588fe9eb83ad1b668c07b11ce44c33794c2b63554d00e6be49a61d"; got != want {
		t.Errorf("aclHeartbeatSig vector drift vs Core:\n got  %s\n want %s", got, want)
	}

	if got, want := aclClusterProof("test-cluster-secret", token), "1d4ece4f626894b29c4b904842dee7795f4ac62dd841eeeeb7a8d979ada847b2"; got != want {
		t.Errorf("aclClusterProof vector drift vs Core:\n got  %s\n want %s", got, want)
	}
}
