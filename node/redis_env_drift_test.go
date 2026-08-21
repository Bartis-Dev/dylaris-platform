package main

import (
	"testing"
)

// A pairing reset replaces the per-node secret while every MC container keeps
// running with the old REDIS_USER/REDIS_PASS baked in. Core provisions the
// shipper user at the new password, so those containers can never authenticate
// again - and nothing about that is visible from outside the container.
func TestRedisEnvDriftSeesACredentialChange(t *testing.T) {
	origNodeID, origSecret := nodeID, nodeSecret
	t.Cleanup(func() { nodeID, nodeSecret = origNodeID, origSecret })

	nodeID = "node-drift-test"
	nodeSecret = []byte("secret-before-the-pairing-reset..")
	const addr = "10.0.0.5:6379"
	running := buildRedisEnv("uuid-1", "sub1", addr)

	if got := redisEnvDrift(running, buildRedisEnv("uuid-1", "sub1", addr)); got != "" {
		t.Fatalf("an unchanged container reported drift in %s", got)
	}

	nodeSecret = []byte("secret-after-the-pairing-reset...")
	if got := redisEnvDrift(running, buildRedisEnv("uuid-1", "sub1", addr)); got != "REDIS_PASS" {
		t.Errorf("drift = %q, want REDIS_PASS: the shipper password is derived from the "+
			"per-node secret, so a reset leaves every running container unable to auth", got)
	}
}

// The identity moves the username as well as the password, and a node CAN come
// back under a new one (bootstrapSecretViaGRPC's identity-replacement branch).
func TestRedisEnvDriftSeesAnIdentityChange(t *testing.T) {
	origNodeID, origSecret := nodeID, nodeSecret
	t.Cleanup(func() { nodeID, nodeSecret = origNodeID, origSecret })

	nodeSecret = []byte("a-stable-secret-for-this-test....")
	nodeID = "node-old-identity"
	const addr = "10.0.0.5:6379"
	running := buildRedisEnv("uuid-1", "", addr)

	nodeID = "node-new-identity"
	if got := redisEnvDrift(running, buildRedisEnv("uuid-1", "", addr)); got != "REDIS_USER" {
		t.Errorf("drift = %q, want REDIS_USER", got)
	}
}

// The address case the function already handled, kept so a refactor cannot
// silently drop it.
func TestRedisEnvDriftSeesAnAddressChange(t *testing.T) {
	origNodeID, origSecret := nodeID, nodeSecret
	t.Cleanup(func() { nodeID, nodeSecret = origNodeID, origSecret })

	nodeID, nodeSecret = "node-addr-test", []byte("a-stable-secret-for-this-test....")
	running := buildRedisEnv("uuid-1", "", "10.0.0.5:6379")
	if got := redisEnvDrift(running, buildRedisEnv("uuid-1", "", "10.0.0.9:6379")); got != "REDIS_ADDR" {
		t.Errorf("drift = %q, want REDIS_ADDR", got)
	}
}

// Only the connection keys count. SUB_SERVER differs on every reconcile pass
// (the expected env is built with an empty one) and must not restart anything.
func TestRedisEnvDriftIgnoresNonConnectionKeys(t *testing.T) {
	origNodeID, origSecret := nodeID, nodeSecret
	t.Cleanup(func() { nodeID, nodeSecret = origNodeID, origSecret })

	nodeID, nodeSecret = "node-sub-test", []byte("a-stable-secret-for-this-test....")
	const addr = "10.0.0.5:6379"
	withSub := buildRedisEnv("uuid-1", "survival", addr)
	withoutSub := buildRedisEnv("uuid-1", "", addr)

	if got := redisEnvDrift(withSub, withoutSub); got != "" {
		t.Errorf("drift = %q; SUB_SERVER is not a connection key and must not trigger a restart", got)
	}
}

func TestEnvValue(t *testing.T) {
	env := []string{"A=1", "BB=", "C=x=y"}
	cases := map[string]string{"A": "1", "BB": "", "C": "x=y", "MISSING": ""}
	for key, want := range cases {
		if got := envValue(env, key); got != want {
			t.Errorf("envValue(%q) = %q, want %q", key, got, want)
		}
	}
}
