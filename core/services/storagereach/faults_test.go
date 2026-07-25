package storagereach

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/redis/go-redis/v9"
)

func TestRecordAndReadFault(t *testing.T) {
	rdb := newReachTestRedis(t)
	ctx := context.Background()

	err := RecordFault(ctx, rdb, Fault{
		CoreID: "core-b", Hostname: "host-b", Status: StatusNotShared,
		MissingPeers: []string{"core-a"}, Since: 100, At: 100,
	})
	if err != nil {
		t.Fatalf("RecordFault: %v", err)
	}

	got, err := Faults(ctx, rdb)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d faults, want 1", len(got))
	}
	if got[0].CoreID != "core-b" || got[0].Status != StatusNotShared {
		t.Fatalf("fault = %+v, want core-b/not-shared", got[0])
	}
	if len(got[0].MissingPeers) != 1 || got[0].MissingPeers[0] != "core-a" {
		t.Errorf("MissingPeers = %v, want [core-a]", got[0].MissingPeers)
	}
}

func TestRecordFault_PreservesSinceAcrossRepeats(t *testing.T) {
	// "Failing since 09:14" is the useful line for an operator; a re-recorded
	// fault that resets Since every tick would always read "just now".
	rdb := newReachTestRedis(t)
	ctx := context.Background()

	if err := RecordFault(ctx, rdb, Fault{CoreID: "core-b", Status: StatusUnreachable, Since: 100, At: 100}); err != nil {
		t.Fatalf("first RecordFault: %v", err)
	}
	if err := RecordFault(ctx, rdb, Fault{CoreID: "core-b", Status: StatusUnreachable, Since: 260, At: 260}); err != nil {
		t.Fatalf("second RecordFault: %v", err)
	}

	got, _ := Faults(ctx, rdb)
	if len(got) != 1 {
		t.Fatalf("got %d faults, want 1", len(got))
	}
	if got[0].Since != 100 {
		t.Errorf("Since = %d, want the ORIGINAL 100", got[0].Since)
	}
	if got[0].At != 260 {
		t.Errorf("At = %d, want the latest 260", got[0].At)
	}
}

func TestRecordFault_ResetsSinceWhenTheStatusChanges(t *testing.T) {
	rdb := newReachTestRedis(t)
	ctx := context.Background()

	_ = RecordFault(ctx, rdb, Fault{CoreID: "core-b", Status: StatusUnreachable, Since: 100, At: 100})
	_ = RecordFault(ctx, rdb, Fault{CoreID: "core-b", Status: StatusWriteDenied, Since: 260, At: 260})

	got, _ := Faults(ctx, rdb)
	if got[0].Status != StatusWriteDenied {
		t.Fatalf("Status = %s, want write-denied", got[0].Status)
	}
	if got[0].Since != 260 {
		t.Errorf("Since = %d, want 260 - a different failure started now", got[0].Since)
	}
}

// TestRecordFault_RealRedisErrorDoesNotOverwriteSince injects a real (non-Nil)
// Redis failure into the prior-record read RecordFault does before writing,
// using the same miniredis SetPreHook seam round_test.go uses for its
// publish-failure tests. err == nil would treat any error, including this
// one, as "no prior record" and write f.Since as given, corrupting the
// operator-facing "failing since" clock. The fix must instead fail the call
// and leave the existing record untouched.
func TestRecordFault_RealRedisErrorDoesNotOverwriteSince(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	if err := RecordFault(ctx, rdb, Fault{CoreID: "core-b", Status: StatusUnreachable, Since: 100, At: 100}); err != nil {
		t.Fatalf("seed RecordFault: %v", err)
	}

	mr.Server().SetPreHook(func(peer *server.Peer, cmd string, args ...string) bool {
		if cmd == "GET" && len(args) > 0 && args[0] == faultKey("core-b") {
			peer.WriteError("SIMULATED: prior-record read failed")
			return true
		}
		return false
	})

	err = RecordFault(ctx, rdb, Fault{CoreID: "core-b", Status: StatusUnreachable, Since: 999, At: 260})
	if err == nil {
		t.Fatal("RecordFault: want an error when the prior-record read fails, got nil")
	}

	mr.Server().SetPreHook(nil)
	got, err := Faults(ctx, rdb)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d faults, want 1 (the pre-existing record must survive untouched)", len(got))
	}
	if got[0].Since != 100 {
		t.Fatalf("Since = %d, want the ORIGINAL 100 - a failed read must not corrupt it", got[0].Since)
	}
	if got[0].At != 100 {
		t.Fatalf("At = %d, want the ORIGINAL 100 too - the write must not have happened at all", got[0].At)
	}
}

func TestClearFault_RemovesIt(t *testing.T) {
	rdb := newReachTestRedis(t)
	ctx := context.Background()
	_ = RecordFault(ctx, rdb, Fault{CoreID: "core-b", Status: StatusNotShared, Since: 1, At: 1})

	if err := ClearFault(ctx, rdb, "core-b"); err != nil {
		t.Fatalf("ClearFault: %v", err)
	}
	got, _ := Faults(ctx, rdb)
	if len(got) != 0 {
		t.Fatalf("got %d faults after clear, want 0", len(got))
	}
}

func TestClearFault_IsIdempotent(t *testing.T) {
	// The periodic tick clears on every healthy pass, so the common case is
	// clearing a fault that is not there.
	rdb := newReachTestRedis(t)
	if err := ClearFault(context.Background(), rdb, "core-never-failed"); err != nil {
		t.Fatalf("ClearFault on a healthy Core = %v, want nil", err)
	}
}

func TestFaults_SortedByCoreID(t *testing.T) {
	rdb := newReachTestRedis(t)
	ctx := context.Background()
	for _, id := range []string{"core-c", "core-a", "core-b"} {
		_ = RecordFault(ctx, rdb, Fault{CoreID: id, Status: StatusNotShared, Since: 1, At: 1})
	}

	got, _ := Faults(ctx, rdb)
	for i, want := range []string{"core-a", "core-b", "core-c"} {
		if got[i].CoreID != want {
			t.Fatalf("Faults[%d] = %s, want %s", i, got[i].CoreID, want)
		}
	}
}

// TestFaults_RespectsScanBudget writes several times faultScanBudget worth of
// valid fault keys directly (bypassing RecordFault's extra round trip, which
// would make seeding thousands of keys needlessly slow) and checks the
// concrete effect the budget exists to guarantee: Faults() returns without
// examining the whole keyspace, so the result is smaller than what was
// written.
//
// miniredis's own SCAN ignores COUNT and always returns every matching key in
// one reply (its cmd_generic.go says so explicitly: "we only ever return all
// at once, so that cursors work"), so it cannot exercise Faults()'s per-batch
// budget check as-is - the inner loop would already have examined every key
// in that single reply before the outer break condition is ever consulted.
// To actually drive the budget, this test installs a miniredis SetPreHook
// (the same seam round_test.go uses for Redis-failure injection) that
// reimplements real, paginated SCAN semantics on top of mr.Keys(), honoring
// the COUNT the production code sends. Test-side seam only; no production
// code changes.
func TestFaults_RespectsScanBudget(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	const totalKeys = faultScanBudget * 3
	pipe := rdb.Pipeline()
	for i := 0; i < totalKeys; i++ {
		f := Fault{CoreID: fmt.Sprintf("core-%05d", i), Status: StatusNotShared, Since: 1, At: 1}
		data, err := json.Marshal(f)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		pipe.Set(ctx, faultKey(f.CoreID), data, faultTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed pipeline: %v", err)
	}

	mr.Server().SetPreHook(func(peer *server.Peer, cmd string, args ...string) bool {
		if cmd != "SCAN" {
			return false
		}
		cursor, _ := strconv.Atoi(args[0])
		match, count := "*", 10
		for i := 1; i < len(args); i++ {
			switch strings.ToUpper(args[i]) {
			case "MATCH":
				i++
				if i < len(args) {
					match = args[i]
				}
			case "COUNT":
				i++
				if i < len(args) {
					count, _ = strconv.Atoi(args[i])
				}
			}
		}
		var matched []string
		for _, k := range mr.Keys() { // mr.Keys() is already sorted
			if ok, _ := path.Match(match, k); ok {
				matched = append(matched, k)
			}
		}
		start := cursor
		if start > len(matched) {
			start = len(matched)
		}
		end := start + count
		if end > len(matched) {
			end = len(matched)
		}
		next := end
		if next >= len(matched) {
			next = 0
		}
		peer.WriteLen(2)
		peer.WriteBulk(strconv.Itoa(next))
		peer.WriteLen(end - start)
		for _, k := range matched[start:end] {
			peer.WriteBulk(k)
		}
		return true
	})

	got, err := Faults(ctx, rdb)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("got 0 faults, want a non-empty partial result bounded by the budget")
	}
	if len(got) >= totalKeys {
		t.Fatalf("got %d faults, want fewer than the %d written - the SCAN budget did not bound the scan", len(got), totalKeys)
	}
	// Slack over the raw budget: the loop only checks the budget after
	// finishing a whole batch, so examined (and therefore len(got), since
	// every seeded key here is a valid record) can land anywhere from the
	// budget up to one extra batch past it.
	if len(got) < faultScanBudget || len(got) > faultScanBudget+100 {
		t.Fatalf("got %d faults, want within one batch of faultScanBudget (%d)", len(got), faultScanBudget)
	}
}

// TestFaults_RealRedisErrorOnGetIsNotSwallowed injects a real (non-Nil) Redis
// failure into the per-key GET inside Faults()'s scan loop, using the same
// miniredis SetPreHook seam TestRecordFault_RealRedisErrorDoesNotOverwriteSince
// uses. The old code treated every GET error, not just redis.Nil, as "the key
// expired between SCAN and GET" and silently skipped it, so a real Redis
// failure would make that Core's fault vanish from the result with Faults()
// still reporting a nil error - the panel would then render a healthier
// fleet than actually exists. The fix must surface the error instead.
func TestFaults_RealRedisErrorOnGetIsNotSwallowed(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	if err := RecordFault(ctx, rdb, Fault{CoreID: "core-a", Status: StatusNotShared, Since: 1, At: 1}); err != nil {
		t.Fatalf("seed RecordFault: %v", err)
	}

	mr.Server().SetPreHook(func(peer *server.Peer, cmd string, args ...string) bool {
		if cmd == "GET" && len(args) > 0 && args[0] == faultKey("core-a") {
			peer.WriteError("SIMULATED: get fault failed")
			return true
		}
		return false
	})
	t.Cleanup(func() { mr.Server().SetPreHook(nil) })

	got, err := Faults(ctx, rdb)
	if err == nil {
		t.Fatalf("Faults: want an error when a per-key GET fails with a real error, got nil (faults = %+v)", got)
	}
}

// TestFaults_SkipsCorruptAndEmptyCoreIDRecords exercises the two defensive
// skips in Faults()'s loop: a value that does not parse as JSON, and a value
// that parses fine but carries no CoreID (RecordFault never produces this -
// it is here to prove the guard, not to model a real caller).
func TestFaults_SkipsCorruptAndEmptyCoreIDRecords(t *testing.T) {
	rdb := newReachTestRedis(t)
	ctx := context.Background()

	if err := RecordFault(ctx, rdb, Fault{CoreID: "core-a", Status: StatusNotShared, Since: 1, At: 1}); err != nil {
		t.Fatalf("RecordFault core-a: %v", err)
	}
	if err := RecordFault(ctx, rdb, Fault{CoreID: "core-b", Status: StatusUnreachable, Since: 1, At: 1}); err != nil {
		t.Fatalf("RecordFault core-b: %v", err)
	}
	if err := rdb.Set(ctx, faultKey("core-corrupt"), "{not-json", faultTTL).Err(); err != nil {
		t.Fatalf("seed corrupt record: %v", err)
	}
	emptyIDData, err := json.Marshal(Fault{CoreID: "", Status: StatusNotShared, Since: 1, At: 1})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := rdb.Set(ctx, faultKey("core-empty-id-key"), emptyIDData, faultTTL).Err(); err != nil {
		t.Fatalf("seed empty-CoreID record: %v", err)
	}

	got, err := Faults(ctx, rdb)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d faults, want 2 (corrupt and empty-CoreID records must be skipped): %+v", len(got), got)
	}
	for _, f := range got {
		if f.CoreID != "core-a" && f.CoreID != "core-b" {
			t.Errorf("unexpected fault in result: %+v", f)
		}
	}
}

func TestLocalStatus_StartsHealthy(t *testing.T) {
	// A Core that has not run its check yet must not gate its own routes:
	// the boot check runs a moment later and will gate if it needs to.
	ls := NewLocalStatus()
	if !ls.Healthy() {
		t.Fatal("a fresh LocalStatus is not healthy; every Core would 503 at boot")
	}
}

// TestLocalStatus_NilReceiverIsSafe uses a genuine nil pointer, the shape an
// unset LocalStatus field on the HTTP app state has before the verifier ever
// runs. Without the l == nil guards in Healthy() and Snapshot(), every
// storage request would panic instead of just serving normally.
func TestLocalStatus_NilReceiverIsSafe(t *testing.T) {
	var ls *LocalStatus
	if !ls.Healthy() {
		t.Fatal("Healthy() on a nil *LocalStatus = false, want true")
	}
	status, detail := ls.Snapshot()
	if status != StatusOK || detail != "" {
		t.Fatalf("Snapshot() on a nil *LocalStatus = %s/%q, want the healthy zero value", status, detail)
	}
}

func TestLocalStatus_GatesOnFailureAndRecovers(t *testing.T) {
	ls := NewLocalStatus()

	ls.Set(StatusNotShared, "cannot see core-a")
	if ls.Healthy() {
		t.Fatal("Healthy = true after a not-shared verdict")
	}
	status, detail := ls.Snapshot()
	if status != StatusNotShared || detail != "cannot see core-a" {
		t.Fatalf("Snapshot = %s/%q, want not-shared with the detail", status, detail)
	}

	ls.Set(StatusOK, "")
	if !ls.Healthy() {
		t.Fatal("Healthy = false after recovery; a transient outage would gate forever")
	}
}

func TestLocalStatus_IsSafeUnderConcurrentAccess(t *testing.T) {
	// The ticker writes it while every gated request reads it.
	ls := NewLocalStatus()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); ls.Set(StatusNotShared, "x") }()
		go func() { defer wg.Done(); _ = ls.Healthy() }()
	}
	wg.Wait()
}
