package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// The per-server migration lock is read by four places that all state the same
// thing: "the lock is held" == "this server is migrating right now"
// (migration_cancel.go's "iff", rebalance_worker.go's migrationLocked,
// servers_lifecycle.go's cancellable, and the orchestrator's own SETNX). That
// equality only holds if the lock lives exactly as long as the migration does,
// which is what these tests pin down.

func migrationLockKeyFor(uuid string) string {
	return fmt.Sprintf("dylaris:server:%s:migration", uuid)
}

func lockExists(t *testing.T, rdb *redis.Client, uuid string) bool {
	t.Helper()
	n, err := rdb.Exists(context.Background(), migrationLockKeyFor(uuid)).Result()
	if err != nil {
		t.Fatalf("EXISTS %s: %v", migrationLockKeyFor(uuid), err)
	}
	return n > 0
}

// A free lock is taken, marked with the requester, and dropped on release.
func TestHoldMigrationLock_TakesAndReleases(t *testing.T) {
	rdb := newQueueTestRedis(t)
	o := &MigrationOrchestrator{redis: rdb}

	release, err := o.holdMigrationLock(context.Background(), "srv-free", "admin",
		time.Second, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("holdMigrationLock on a free lock: %v", err)
	}
	if !lockExists(t, rdb, "srv-free") {
		t.Fatal("lock key absent while held")
	}
	got, err := rdb.Get(context.Background(), migrationLockKeyFor("srv-free")).Result()
	if err != nil {
		t.Fatalf("GET lock: %v", err)
	}
	if got != "admin" {
		t.Errorf("lock value = %q, want the requester %q", got, "admin")
	}

	release()
	if lockExists(t, rdb, "srv-free") {
		t.Error("lock key still present after release")
	}
}

// The migration outlives the lock's TTL by design: the orchestrator's own
// budgets allow 90s stop + 5m staging + 30m transfer on the LAN path (and an
// hour per leg on the BYON R2 path). An unrefreshed lock therefore disappears
// under a migration that is still running, and every reader above then reports
// "not migrating" - the cancel endpoint answers 409 and the panel hides the
// button, and the rebalance worker stops skipping the server.
//
// Deleting the key out from under the holder is a TTL expiry the test can time.
func TestHoldMigrationLock_RefreshesWhileTheMigrationRuns(t *testing.T) {
	rdb := newQueueTestRedis(t)
	o := &MigrationOrchestrator{redis: rdb}

	const ttl = 90 * time.Millisecond
	release, err := o.holdMigrationLock(context.Background(), "srv-long", "admin",
		ttl, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("holdMigrationLock: %v", err)
	}
	defer release()

	if err := rdb.Del(context.Background(), migrationLockKeyFor("srv-long")).Err(); err != nil {
		t.Fatalf("DEL lock: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !lockExists(t, rdb, "srv-long") {
		if time.Now().After(deadline) {
			t.Fatal("the lock was not refreshed: it stays gone while the migration is still running")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The refresher must stop when the migration does, or a released lock would be
// resurrected by a tick that was already in flight.
func TestHoldMigrationLock_StopsRefreshingAfterRelease(t *testing.T) {
	rdb := newQueueTestRedis(t)
	o := &MigrationOrchestrator{redis: rdb}

	const ttl = 60 * time.Millisecond
	release, err := o.holdMigrationLock(context.Background(), "srv-done", "admin",
		ttl, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("holdMigrationLock: %v", err)
	}
	release()

	time.Sleep(4 * ttl)
	if lockExists(t, rdb, "srv-done") {
		t.Error("the refresher put the lock back after release")
	}
}

// The regression. A Core that dies mid-migration leaves its lock behind, and the
// queue redelivers the request the moment the process is back - which is well
// inside the lock's remaining life. Bailing there is not a pause, it is a drop:
// the handler returns nil, the entry is ACKed and dedup-marked, and nothing
// retries it. The server stays stopped (stopServer set desired_state=stopped, so
// the node reconciler leaves it down) with no record but a status key nobody
// reads.
//
// So a held lock must be waited out, not skipped over.
func TestHoldMigrationLock_WaitsOutAStaleLockInsteadOfDroppingTheRequest(t *testing.T) {
	rdb := newQueueTestRedis(t)
	o := &MigrationOrchestrator{redis: rdb}

	// The dead Core's lock, still in Redis.
	if err := rdb.Set(context.Background(), migrationLockKeyFor("srv-stale"), "admin", 0).Err(); err != nil {
		t.Fatalf("seed the stale lock: %v", err)
	}

	type result struct {
		release func()
		err     error
	}
	done := make(chan result, 1)
	go func() {
		release, err := o.holdMigrationLock(context.Background(), "srv-stale", "admin",
			time.Second, 3*time.Second, 10*time.Millisecond)
		done <- result{release, err}
	}()

	// The stale lock reaching its TTL.
	time.Sleep(100 * time.Millisecond)
	if err := rdb.Del(context.Background(), migrationLockKeyFor("srv-stale")).Err(); err != nil {
		t.Fatalf("expire the stale lock: %v", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("the recovery delivery gave up on a stale lock instead of waiting it out: %v", got.err)
		}
		got.release()
	case <-time.After(3 * time.Second):
		t.Fatal("holdMigrationLock never returned after the stale lock cleared")
	}
}

// The other side of that wait: a lock that keeps being refreshed belongs to a
// live migration on another Core, and this delivery must give up rather than
// block the serialized queue forever. errMigrationLockHeld is what tells the
// caller to skip quietly instead of reporting a failure.
func TestHoldMigrationLock_GivesUpOnALockThatNeverClears(t *testing.T) {
	rdb := newQueueTestRedis(t)
	o := &MigrationOrchestrator{redis: rdb}

	if err := rdb.Set(context.Background(), migrationLockKeyFor("srv-busy"), "other-core", 0).Err(); err != nil {
		t.Fatalf("seed the live lock: %v", err)
	}

	start := time.Now()
	release, err := o.holdMigrationLock(context.Background(), "srv-busy", "admin",
		time.Second, 200*time.Millisecond, 10*time.Millisecond)
	if !errors.Is(err, errMigrationLockHeld) {
		t.Fatalf("err = %v, want errMigrationLockHeld", err)
	}
	if release != nil {
		t.Error("a release func was returned for a lock that was never acquired")
	}
	if waited := time.Since(start); waited < 200*time.Millisecond {
		t.Errorf("gave up after %s, want at least the full %s wait", waited, 200*time.Millisecond)
	}
	if got := rdb.Get(context.Background(), migrationLockKeyFor("srv-busy")).Val(); got != "other-core" {
		t.Errorf("the other Core's lock was overwritten: %q", got)
	}
}

// Losing leadership while waiting out a lock is not this migration failing.
// Migrate distinguishes the two on the error, so the wait must surface the
// cancellation rather than flattening it into errMigrationLockHeld: the request
// has to stay pending for the next leader, not be reported failed.
func TestHoldMigrationLock_SurfacesCancellationSeparatelyFromContention(t *testing.T) {
	rdb := newQueueTestRedis(t)
	o := &MigrationOrchestrator{redis: rdb}

	if err := rdb.Set(context.Background(), migrationLockKeyFor("srv-handover"), "other-core", 0).Err(); err != nil {
		t.Fatalf("seed the lock: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	release, err := o.holdMigrationLock(ctx, "srv-handover", "admin",
		time.Second, 10*time.Second, 10*time.Millisecond)
	if release != nil {
		t.Error("a release func was returned for a lock that was never acquired")
	}
	if errors.Is(err, errMigrationLockHeld) {
		t.Fatal("a cancellation was reported as lock contention, which Migrate would record as a failed migration")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// The invariant that makes the wait worth anything. If the budget were shorter
// than the TTL, a stale lock would still be outlived by the wait only sometimes,
// which is the dropped-request bug with extra steps.
func TestMigrationLockWaitOutlastsTheLockTTL(t *testing.T) {
	if migrationLockWait <= migrationLockTTL {
		t.Fatalf("migrationLockWait = %s, must exceed the %s lock TTL so a stale lock is always waited out",
			migrationLockWait, migrationLockTTL)
	}
}
