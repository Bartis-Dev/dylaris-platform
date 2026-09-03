package errlog

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func expiryRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()}), mr
}

// The instance id is usually a container hostname, so every redeploy starts a
// new stream and abandons the old one. Measured in production on 2026-09-03: 88
// streams, 68 of them the hub's against two live instances, every one of them
// with TTL -1. Nothing was going to remove them, and the panel's Errors screen
// reads all of them on every load.
func TestAWrittenStreamIsGivenAnExpiry(t *testing.T) {
	rdb, _ := expiryRedis(t)
	l := New(rdb, "core", "instance-1")

	l.Error("test", "something went wrong")

	ttl, err := rdb.TTL(context.Background(), l.StreamKey()).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("TTL = %v, want a positive expiry - a stream with none never ages out", ttl)
	}
	if ttl > streamTTL {
		t.Errorf("TTL = %v, longer than the %v window it is supposed to set", ttl, streamTTL)
	}
}

// The load-bearing half, and the reason the expiry is set on every write rather
// than once when the logger is built: XADD does not refresh a TTL. Set once, a
// busy stream would expire out from under a component that is still writing to
// it, which would be worse than the leak - it would drop the errors of the one
// component still running.
func TestWritingAgainRefreshesTheExpiry(t *testing.T) {
	rdb, mr := expiryRedis(t)
	ctx := context.Background()
	l := New(rdb, "core", "instance-1")

	l.Error("test", "first")
	mr.FastForward(streamTTL - time.Hour)

	nearlyGone, err := rdb.TTL(ctx, l.StreamKey()).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if nearlyGone > 2*time.Hour {
		t.Fatalf("setup: expected the stream to be close to expiry, TTL = %v", nearlyGone)
	}

	l.Error("test", "second")

	refreshed, err := rdb.TTL(ctx, l.StreamKey()).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if refreshed <= nearlyGone {
		t.Fatalf("TTL = %v after a second write, was %v before it - a live stream must not age out", refreshed, nearlyGone)
	}
}
