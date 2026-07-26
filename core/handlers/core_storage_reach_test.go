package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"dylaris-core/services/storagereach"
	"dylaris-core/storage"
)

// Redis seeding for these tests reuses multiCoreRedis and coreStorageFakeStore
// (core_storage_multicore_test.go / core_storage_builder_test.go) rather than
// declaring a second heartbeat-seeding helper for the same package.

func TestCoreStorageToReachConfig_PathBackend(t *testing.T) {
	got := CoreStorageToReachConfig(CoreStorageConfig{Backend: "path", Path: "/mnt/shared"})
	if got.Backend != "path" || got.Path != "/mnt/shared" {
		t.Fatalf("config = %+v, want the path backend carried over", got)
	}
}

func TestCoreStorageToReachConfig_S3CarriesTheCredential(t *testing.T) {
	// The participant builds a real client from this, so the secret has to
	// travel. If it stops travelling, every peer reports unreachable.
	got := CoreStorageToReachConfig(CoreStorageConfig{
		Backend: "s3", S3Endpoint: "https://s3.example", S3Bucket: "b",
		S3Region: "eu", S3AccessKey: "AK", S3SecretKey: "SK", S3Prefix: "p", S3PathStyle: true,
	})
	if got.S3Bucket != "b" || got.S3AccessKey != "AK" || got.S3SecretKey != "SK" {
		t.Fatalf("config = %+v, want the s3 identity and credential carried over", got)
	}
	if !got.S3PathStyle || got.S3Prefix != "p" || got.S3Region != "eu" || got.S3Endpoint != "https://s3.example" {
		t.Fatalf("config = %+v, want every s3 field carried over", got)
	}
}

func TestCheckSharedStorageReachable_SingleCoreSkipsTheRound(t *testing.T) {
	// One Core cannot fail a sharing check, and running a probe on every
	// single-instance save would be pure latency.
	rdb := multiCoreRedis(t, "core-a")
	s := &AppState{Redis: rdb, Store: &coreStorageFakeStore{values: map[string]string{}}}

	if err := s.checkSharedStorageReachable(context.Background(),
		CoreStorageConfig{Backend: "path", Path: "/mnt/shared"}); err != nil {
		t.Fatalf("checkSharedStorageReachable on a single Core = %v, want nil", err)
	}
}

// TestCheckSharedStorageReachable_ZeroOnlineCoresIsTreatedAsOne pins the other
// half of the "0 and 1 are both not-more-than-one" short-circuit: a fresh
// install whose own heartbeat has not landed in Redis yet must still be able
// to save its storage settings, not be refused because the online count reads
// as zero. This case existed in the old count-only guard's table and was
// dropped when that guard was replaced; the behavior itself was never
// removed (see the len(online) <= 1 check above), only its test coverage.
func TestCheckSharedStorageReachable_ZeroOnlineCoresIsTreatedAsOne(t *testing.T) {
	rdb := multiCoreRedis(t) // no heartbeats seeded: the online count is 0
	s := &AppState{Redis: rdb, Store: &coreStorageFakeStore{values: map[string]string{}}}

	if err := s.checkSharedStorageReachable(context.Background(),
		CoreStorageConfig{Backend: "path", Path: "/mnt/shared"}); err != nil {
		t.Fatalf("checkSharedStorageReachable with zero online Cores = %v, want nil (short-circuit)", err)
	}
}

func TestCheckSharedStorageReachable_RefusesWhenAPeerNeverReports(t *testing.T) {
	// Two Cores heartbeating, only one of them running: fail-closed. core-a
	// (this process) gets a real, working provider so its OWN participation
	// succeeds; core-b never runs one, which is what proves the round - not
	// the count - is what decides the outcome.
	rdb := multiCoreRedis(t, "core-a", "core-b")
	s := &AppState{
		Redis: rdb,
		Store: &coreStorageFakeStore{values: map[string]string{}},
		StorageReach: storagereach.NewService(storagereach.ServiceDeps{
			Redis: rdb, CoreID: "core-a", NewProvider: sharedLocalFactory(t.TempDir()),
		}),
	}
	s.reachRoundDeadline = 150 * time.Millisecond

	err := s.checkSharedStorageReachable(context.Background(),
		CoreStorageConfig{Backend: "path", Path: t.TempDir()})

	if err == nil {
		t.Fatal("the save was allowed although core-b never proved access")
	}
	var refusal *sharedStorageRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %T, want *sharedStorageRefusal", err)
	}
	if refusal.status != http.StatusConflict {
		t.Errorf("status = %d, want 409", refusal.status)
	}
	if refusal.result == nil {
		t.Fatal("the refusal carries no RoundResult; the panel cannot name the failing Core")
	}
	found := false
	for _, r := range refusal.result.Results {
		if r.CoreID == "core-b" && r.Status == storagereach.StatusNoResponse {
			found = true
		}
	}
	if !found {
		t.Errorf("results = %+v, want core-b as no-response", refusal.result.Results)
	}
}

func TestCheckSharedStorageReachable_RedisFailureRefuses(t *testing.T) {
	// "Could not verify" must never read as "verified". This mirrors the
	// behaviour the count-based guard already had.
	s := &AppState{Store: &coreStorageFakeStore{values: map[string]string{}}} // no Redis
	err := s.checkSharedStorageReachable(context.Background(),
		CoreStorageConfig{Backend: "path", Path: "/mnt/shared"})
	if err == nil {
		t.Fatal("a save was allowed while the online-Core set could not be read")
	}
}

// TestCheckSharedStorageReachable_RefusesWhileAnotherRoundIsLocked is carry-over
// check #2: RunRound's own cleanup (round.go) deletes the single global
// "current round" Redis key unconditionally, without checking it still points
// at ITS round. Two rounds racing would let one delete the other's still-open
// round out from under it. The lock this test seeds simulates a round already
// in flight - from this Core, or (just as easily) from a different Core
// replica the load balancer sent a second concurrent save to - and proves a
// second round refuses outright instead of racing the first.
func TestCheckSharedStorageReachable_RefusesWhileAnotherRoundIsLocked(t *testing.T) {
	rdb := multiCoreRedis(t, "core-a", "core-b")
	if err := rdb.SetNX(context.Background(), reachRoundLockKey, "1", time.Minute).Err(); err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	s := &AppState{
		Redis: rdb,
		Store: &coreStorageFakeStore{values: map[string]string{}},
		StorageReach: storagereach.NewService(storagereach.ServiceDeps{
			Redis: rdb, CoreID: "core-a", NewProvider: sharedLocalFactory(t.TempDir()),
		}),
	}

	err := s.checkSharedStorageReachable(context.Background(),
		CoreStorageConfig{Backend: "path", Path: t.TempDir()})

	if err == nil {
		t.Fatal("the save proceeded while another round was already locked")
	}
	var refusal *sharedStorageRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %T, want *sharedStorageRefusal", err)
	}
	if refusal.status != http.StatusConflict {
		t.Errorf("status = %d, want 409", refusal.status)
	}
}

// TestCheckSharedStorageReachable_ReleasesTheLockAfterARound closes the loop on
// the lock above: a round that finishes - success or a fail-closed timeout -
// must not leave the cluster-wide lock behind, or every later save would
// refuse forever.
func TestCheckSharedStorageReachable_ReleasesTheLockAfterARound(t *testing.T) {
	rdb := multiCoreRedis(t, "core-a", "core-b")
	s := &AppState{
		Redis: rdb,
		Store: &coreStorageFakeStore{values: map[string]string{}},
		StorageReach: storagereach.NewService(storagereach.ServiceDeps{
			Redis: rdb, CoreID: "core-a", NewProvider: sharedLocalFactory(t.TempDir()),
		}),
		reachRoundDeadline: 150 * time.Millisecond,
	}

	_ = s.checkSharedStorageReachable(context.Background(), CoreStorageConfig{Backend: "path", Path: t.TempDir()})

	if _, err := rdb.Get(context.Background(), reachRoundLockKey).Result(); err != redis.Nil {
		t.Errorf("the config-round lock was not released after the round finished (err = %v)", err)
	}
}

// TestStorageReachRoundLockRelease_StaleHolderCannotDeleteANewerLock pins the
// compare-and-delete fix: the lock value is a per-attempt token, not a shared
// constant, so a holder releasing after its own TTL has already expired must
// not delete a DIFFERENT holder's lock that has since taken the key. A plain
// unconditional Del would delete whatever is there, deleting a later round's
// still-open lock out from under it - exactly the unconditional-delete bug
// class this lock exists to defend against in RunRound's own cleanup.
func TestStorageReachRoundLockRelease_StaleHolderCannotDeleteANewerLock(t *testing.T) {
	rdb := multiCoreRedis(t, "core-a")
	ctx := context.Background()

	// Simulate a newer holder already owning the key - as if the stale
	// holder's TTL expired, a later round acquired the lock with its own
	// token, and only THEN does the stale holder's deferred release finally
	// run.
	if err := rdb.Set(ctx, reachRoundLockKey, "newer-token", time.Minute).Err(); err != nil {
		t.Fatalf("seed newer lock: %v", err)
	}

	if err := releaseReachRoundLock(ctx, rdb, "stale-token"); err != nil {
		t.Fatalf("releaseReachRoundLock: %v", err)
	}

	got, err := rdb.Get(ctx, reachRoundLockKey).Result()
	if err != nil {
		t.Fatalf("the newer lock is gone after a stale-token release (err = %v); want it untouched", err)
	}
	if got != "newer-token" {
		t.Fatalf("lock value = %q, want %q untouched", got, "newer-token")
	}
}

// TestStorageReachRoundLockRelease_MatchingTokenDeletes is the positive
// control for the test above: a release presenting the SAME token the lock
// was set with must still actually free it, or every save would refuse
// forever.
func TestStorageReachRoundLockRelease_MatchingTokenDeletes(t *testing.T) {
	rdb := multiCoreRedis(t, "core-a")
	ctx := context.Background()

	if err := rdb.Set(ctx, reachRoundLockKey, "my-token", time.Minute).Err(); err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	if err := releaseReachRoundLock(ctx, rdb, "my-token"); err != nil {
		t.Fatalf("releaseReachRoundLock: %v", err)
	}
	if _, err := rdb.Get(ctx, reachRoundLockKey).Result(); err != redis.Nil {
		t.Errorf("lock still present after a matching-token release (err = %v)", err)
	}
}

// blockingProvider models a wedged mount for the concurrency test below:
// WriteFile blocks until the test releases it, exactly the RunRound -> Probe
// -> prov.WriteFile chain Fix 1 is about (storage/limiter.go's Gate.Run runs
// WriteFile INLINE with no deadline, so a real wedged mount blocks this call
// forever).
//
// storage.StorageProvider is embedded nil on purpose: any method other than
// the two implemented below is one this fake never expects to be called, and
// a nil-pointer panic says so loudly instead of quietly succeeding.
type blockingProvider struct {
	storage.StorageProvider
	// entered is signalled once WriteFile is called, so the test can wait for
	// the round to actually be inside the hung call before it measures how
	// fast a second, concurrent call refuses.
	entered chan struct{}
	release chan struct{}
}

func newBlockingProvider() *blockingProvider {
	return &blockingProvider{entered: make(chan struct{}, 4), release: make(chan struct{})}
}

func (p *blockingProvider) WriteFile(context.Context, string, io.Reader) error {
	p.entered <- struct{}{}
	<-p.release
	return errors.New("storage released")
}

// ListFiles is reached once WriteFile finally returns, so it has to answer;
// erroring here keeps Probe's retry loop away from the nil embedded provider.
func (p *blockingProvider) ListFiles(context.Context, string) ([]storage.FileInfo, error) {
	return nil, errors.New("storage released")
}

// DeletePath is reached by RunRound's unconditional CleanupRound defer once
// the round finishes, success or not; a no-op keeps that away from the nil
// embedded provider too.
func (p *blockingProvider) DeletePath(context.Context, string) error {
	return nil
}

// TestCheckSharedStorageReachable_SecondConcurrentCallIsRefusedNotQueued pins
// Fix 1: with a real round wedged inside RunRound (a hung WriteFile, the
// documented failure mode of Gate.Run), a second concurrent call must be
// refused immediately with the same 409 the Redis lock branch returns, not
// queued behind the first until it eventually gives up. Restoring
// reachRoundMu.Lock() in place of TryLock() turns this test into a hang,
// which is the discriminating proof that TryLock is what fixes it.
func TestCheckSharedStorageReachable_SecondConcurrentCallIsRefusedNotQueued(t *testing.T) {
	rdb := multiCoreRedis(t, "core-a", "core-b")
	prov := newBlockingProvider()
	s := &AppState{
		Redis: rdb,
		Store: &coreStorageFakeStore{values: map[string]string{}},
		StorageReach: storagereach.NewService(storagereach.ServiceDeps{
			Redis: rdb, CoreID: "core-a",
			NewProvider: func(storagereach.Config) (storage.StorageProvider, error) { return prov, nil },
		}),
		reachRoundDeadline: 150 * time.Millisecond,
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- s.checkSharedStorageReachable(context.Background(),
			CoreStorageConfig{Backend: "path", Path: "/mnt/shared"})
	}()

	select {
	case <-prov.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the first call never reached the hung write; the test setup is broken")
	}

	secondStart := time.Now()
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- s.checkSharedStorageReachable(context.Background(),
			CoreStorageConfig{Backend: "path", Path: "/mnt/shared"})
	}()

	select {
	case err := <-secondDone:
		if elapsed := time.Since(secondStart); elapsed > time.Second {
			t.Fatalf("the second call took %s to return; it queued behind the first instead of refusing immediately", elapsed)
		}
		var refusal *sharedStorageRefusal
		if !errors.As(err, &refusal) {
			t.Fatalf("second call err = %T, want *sharedStorageRefusal", err)
		}
		if refusal.status != http.StatusConflict {
			t.Errorf("second call status = %d, want 409", refusal.status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second call never returned: it queued behind the first instead of being refused")
	}

	close(prov.release)
	select {
	case err := <-firstDone:
		if err == nil {
			t.Error("the first call should have failed - its own write never succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first call did not finish after being released")
	}
}

func TestStorageReachStatusHandler_ReturnsFaults(t *testing.T) {
	rdb := multiCoreRedis(t, "core-a")
	ctx := context.Background()
	if err := storagereach.RecordFault(ctx, rdb, storagereach.Fault{
		CoreID: "core-b", Status: storagereach.StatusNotShared,
		MissingPeers: []string{"core-a"}, Since: 1, At: 1,
	}); err != nil {
		t.Fatalf("RecordFault: %v", err)
	}
	h := NewCoreStorageHandler(&AppState{Redis: rdb, Store: &coreStorageFakeStore{values: map[string]string{}}})

	rec := httptest.NewRecorder()
	h.StorageReachStatus(rec, httptest.NewRequest("GET", "/api/settings/storage-reach", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Faults []storagereach.Fault `json:"faults"`
		Online []string             `json:"onlineCores"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	if len(body.Faults) != 1 || body.Faults[0].CoreID != "core-b" {
		t.Fatalf("faults = %+v, want one for core-b", body.Faults)
	}
	if len(body.Online) != 1 || body.Online[0] != "core-a" {
		t.Errorf("onlineCores = %v, want [core-a]", body.Online)
	}
}

// storageReachDepsCounters counts every call into the three ServiceDeps hooks
// that SelfCheck and RunRound touch, so the test below can assert an
// INVOCATION count instead of an outcome.
//
// An outcome-based check (before/after status, or "no round key landed in
// Redis") can be fooled two different ways: an unconfigured backend makes
// SelfCheck produce the exact same idempotent ok result every time (so a
// repeat call is invisible in a status diff), and checkSharedStorageReachable
// short-circuits on "not more than one online Core" before it ever reaches
// the round machinery (so a wrongly-wired round call leaves no round key
// either). A call count sidesteps both: ConfigFor is the first thing observe
// does inside SelfCheck regardless of what it returns, and NewProvider is
// what RunRound's Coordinator calls for ITS OWN participation regardless of
// how many peers there are - so both fire on the very first line of the code
// path they claim to guard, before either masking condition gets a chance to
// apply.
type storageReachDepsCounters struct {
	configFor   atomic.Int64
	onlineCores atomic.Int64
	newProvider atomic.Int64
}

// TestStorageReachStatusHandler_DoesNotSelfCheckOrStartARound pins the
// read-only doc comment on StorageReachStatus with a REAL, started verifier
// service rather than trusting the comment alone: Service.SelfCheck is
// documented as unsafe to call concurrently with the service's own running
// Start loop (both end in apply, whose state is single-writer by convention,
// not mutex-guarded), and a round must only ever be started by an actual
// config save. If a future edit ever wires the handler into either path, this
// fails.
func TestStorageReachStatusHandler_DoesNotSelfCheckOrStartARound(t *testing.T) {
	// Two online Cores, not one: checkSharedStorageReachable treats "not more
	// than one online Core" as nothing-to-prove and returns immediately. With
	// only one Core seeded, a regression that wired the status handler into
	// that round check would never even reach the round machinery, and every
	// assertion below would still pass on a broken handler. This reads through
	// services.OnlineCoreIDs (the real Redis heartbeat set), independently of
	// the ServiceDeps.OnlineCores fake below.
	rdb := multiCoreRedis(t, "core-a", "core-b")
	counters := &storageReachDepsCounters{}
	providerRoot := t.TempDir()
	svc := storagereach.NewService(storagereach.ServiceDeps{
		Redis:  rdb,
		CoreID: "core-a",
		// Unconfigured storage keeps the boot self-check a fast no-op (observe
		// returns immediately without touching the provider or Redis, see
		// service.go) so the baseline snapshot below is not racing real
		// filesystem I/O. Counted anyway: ConfigFor is called unconditionally
		// as observe's first step, configured or not, so the counter alone
		// (not what it returns) is what proves whether SelfCheck ran again.
		ConfigFor: func() (storagereach.Config, bool) {
			counters.configFor.Add(1)
			return storagereach.Config{}, false
		},
		OnlineCores: func(ctx context.Context) ([]string, error) {
			counters.onlineCores.Add(1)
			return []string{"core-a"}, nil
		},
		NewProvider: func(cfg storagereach.Config) (storage.StorageProvider, error) {
			counters.newProvider.Add(1)
			return &storage.LocalProvider{BasePath: providerRoot}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)

	// Give the boot self-check goroutine time to run and settle so the
	// baseline below isn't racing it - a race there would be a false positive
	// for "the handler changed nothing".
	time.Sleep(50 * time.Millisecond)
	statusBefore, detailBefore := svc.Status().Snapshot()
	configForBefore := counters.configFor.Load()
	onlineCoresBefore := counters.onlineCores.Load()
	newProviderBefore := counters.newProvider.Load()

	h := NewCoreStorageHandler(&AppState{
		Redis: rdb, Store: &coreStorageFakeStore{values: map[string]string{}}, StorageReach: svc,
	})
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.StorageReachStatus(rec, httptest.NewRequest("GET", "/api/settings/storage-reach", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, want 200 (%s)", i, rec.Code, rec.Body.String())
		}
	}

	statusAfter, detailAfter := svc.Status().Snapshot()
	if statusAfter != statusBefore || detailAfter != detailBefore {
		t.Fatalf("status endpoint changed the local verdict: before = %s/%q, after = %s/%q",
			statusBefore, detailBefore, statusAfter, detailAfter)
	}

	if got := counters.configFor.Load(); got != configForBefore {
		t.Fatalf("ConfigFor was called %d time(s) by the status endpoint (baseline %d): the handler ran a self-check",
			got-configForBefore, configForBefore)
	}
	if got := counters.onlineCores.Load(); got != onlineCoresBefore {
		t.Fatalf("OnlineCores was called %d time(s) by the status endpoint (baseline %d): the handler ran a self-check or a round",
			got-onlineCoresBefore, onlineCoresBefore)
	}
	if got := counters.newProvider.Load(); got != newProviderBefore {
		t.Fatalf("NewProvider was called %d time(s) by the status endpoint (baseline %d): the handler opened a storage provider, meaning it ran a self-check or started a round",
			got-newProviderBefore, newProviderBefore)
	}

	pending, err := storagereach.PendingRoundID(ctx, rdb)
	if err != nil {
		t.Fatalf("PendingRoundID: %v", err)
	}
	if pending != "" {
		t.Fatalf("a round was started by a status-only read: pending round id = %q", pending)
	}
}
