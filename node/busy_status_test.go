package main

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newBusyRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, mr
}

// TestHoldBusyStatusIsAReconcilerProtectedStatus ties the marker to the guard it
// exists for. protectedStatuses is what stops the reconciler from "restarting a
// crashed container"; writing any other word there would leave the marker
// looking correct while protecting nothing.
func TestHoldBusyStatusIsAReconcilerProtectedStatus(t *testing.T) {
	rdb, _ := newBusyRedis(t)
	release := holdBusyStatus(rdb, "srv-uuid", "installing", busyStatusTTL)
	defer release()

	got, err := rdb.Get(context.Background(), "dylaris:server:srv-uuid:status").Result()
	if err != nil {
		t.Fatalf("status key not set: %v", err)
	}
	if !protectedStatuses[got] {
		t.Fatalf("holdBusyStatus wrote %q, which is not in protectedStatuses - the reconciler would restart the container anyway", got)
	}
}

// TestHoldBusyStatusOutlivesItsTTL: the marker's TTL is shorter than the work it
// covers. A Paper reinstall measured 10s, but Forge and NeoForge run their
// installer in a one-shot container and take minutes, so a set-once marker would
// expire mid-install and hand the container back to the reconciler.
func TestHoldBusyStatusOutlivesItsTTL(t *testing.T) {
	rdb, mr := newBusyRedis(t)
	key := "dylaris:server:srv-uuid:status"
	// A short TTL so the wall-clock refresh ticker (ttl/3) actually fires within
	// the test. miniredis expiry advances only via FastForward, so the two clocks
	// are driven separately: jump most of a TTL, then give the ticker real time
	// to refresh, and repeat past several TTLs' worth.
	const ttl = 300 * time.Millisecond
	release := holdBusyStatus(rdb, "srv-uuid", "installing", ttl)
	defer release()

	for range 5 {
		mr.FastForward(ttl * 2 / 3)
		time.Sleep(ttl/3 + 40*time.Millisecond)
	}
	if _, err := rdb.Get(context.Background(), key).Result(); err != nil {
		t.Fatalf("status marker expired during the work it was meant to cover: %v", err)
	}

	// Control: without the refresh the same elapsed time DOES expire it, so the
	// assertion above is about the ticker and not about miniredis being lenient.
	rdb.Set(context.Background(), "dylaris:server:other:status", "installing", ttl)
	mr.FastForward(ttl * 2)
	if _, err := rdb.Get(context.Background(), "dylaris:server:other:status").Result(); err == nil {
		t.Fatal("a set-once marker survived past its TTL; this test cannot prove the refresh works")
	}
}

// TestHoldBusyStatusStopsRefreshingAfterRelease: once the work is done the
// caller writes the real terminal status, and this must not overwrite it again.
func TestHoldBusyStatusStopsRefreshingAfterRelease(t *testing.T) {
	rdb, _ := newBusyRedis(t)
	ctx := context.Background()
	key := "dylaris:server:srv-uuid:status"

	release := holdBusyStatus(rdb, "srv-uuid", "installing", 150*time.Millisecond)
	release()

	// What the reinstall handler does on every exit path.
	rdb.Set(ctx, key, "stopped", 30*time.Second)
	time.Sleep(150 * time.Millisecond) // longer than the refresh interval would be

	if got, _ := rdb.Get(ctx, key).Result(); got != "stopped" {
		t.Fatalf("status = %q after release, want stopped - a stale refresh overwrote the terminal status", got)
	}
}
