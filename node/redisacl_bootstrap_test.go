package main

import (
	"context"
	"testing"
	"time"
)

// setNodeSecretForTest writes the secret directly. setNodeSecret is not usable
// here: it deliberately log.Fatal's the agent when the value CHANGES, because a
// rotated secret invalidates the Redis credentials in flight.
func setNodeSecretForTest(s []byte) {
	nodeSecretMu.Lock()
	nodeSecret = s
	nodeSecretMu.Unlock()
}

// The beam LAN certificate is keyed on the per-node secret, which arrives over
// the authenticated gRPC bootstrap and can therefore still be empty when the
// listener goroutine starts. Deriving from an empty secret disabled the fast
// path for the whole process lifetime, so the listener waits instead.
func TestWaitForNodeSecret(t *testing.T) {
	t.Cleanup(func() { setNodeSecretForTest(nil) })

	t.Run("returns as soon as the secret lands", func(t *testing.T) {
		setNodeSecretForTest(nil)
		go func() {
			time.Sleep(150 * time.Millisecond)
			setNodeSecretForTest([]byte{1, 2, 3})
		}()
		got, ok := waitForNodeSecret(context.Background(), 5*time.Second)
		if !ok || len(got) != 3 {
			t.Fatalf("waitForNodeSecret = %v, %v; want the secret once it lands", got, ok)
		}
	})

	t.Run("returns immediately when the secret is already set", func(t *testing.T) {
		setNodeSecretForTest([]byte{9})
		start := time.Now()
		if _, ok := waitForNodeSecret(context.Background(), 5*time.Second); !ok {
			t.Fatal("expected the already-set secret")
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("waited %v for a secret that was already there", elapsed)
		}
	})

	t.Run("gives up on timeout rather than blocking forever", func(t *testing.T) {
		setNodeSecretForTest(nil)
		if _, ok := waitForNodeSecret(context.Background(), 50*time.Millisecond); ok {
			t.Error("expected a timeout with no secret set")
		}
	})

	t.Run("honours context cancellation", func(t *testing.T) {
		setNodeSecretForTest(nil)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(50 * time.Millisecond); cancel() }()
		if _, ok := waitForNodeSecret(ctx, time.Minute); ok {
			t.Error("expected cancellation to stop the wait")
		}
	})
}
