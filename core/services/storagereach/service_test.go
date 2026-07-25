package storagereach

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"dylaris-core/storage"

	"github.com/redis/go-redis/v9"
)

func testDeps(t *testing.T, root, coreID string, online []string) ServiceDeps {
	t.Helper()
	return ServiceDeps{
		Redis:       newReachTestRedis(t),
		CoreID:      coreID,
		NewProvider: sharedFactory(root),
		ConfigFor:   func() (Config, bool) { return Config{Backend: "path", Path: "/mnt/shared"}, true },
		OnlineCores: func(context.Context) ([]string, error) { return online, nil },
	}
}

func TestSelfCheck_SoloCoreIsOK(t *testing.T) {
	svc := NewService(testDeps(t, t.TempDir(), "core-a", []string{"core-a"}))

	res := svc.SelfCheck(context.Background())

	if res.Status != StatusOK {
		t.Fatalf("status = %s, want ok", res.Status)
	}
	if !svc.Status().Healthy() {
		t.Fatal("LocalStatus is unhealthy after a passing self-check")
	}
}

func TestSelfCheck_UnconfiguredStorageIsNotAFault(t *testing.T) {
	// A fresh install has no storage configured. RequireCoreStorageConfigured
	// already handles that; flagging it here would put a red fleet banner on
	// every new deployment.
	deps := testDeps(t, t.TempDir(), "core-a", []string{"core-a"})
	deps.ConfigFor = func() (Config, bool) { return Config{}, false }
	svc := NewService(deps)

	res := svc.SelfCheck(context.Background())

	if res.Status != StatusOK {
		t.Fatalf("status = %s, want ok for an unconfigured Core", res.Status)
	}
	faults, _ := Faults(context.Background(), deps.Redis)
	if len(faults) != 0 {
		t.Fatalf("recorded %d faults for an unconfigured Core, want 0", len(faults))
	}
}

func TestSelfCheck_FirstNotSharedRecordsAFaultButDoesNotGate(t *testing.T) {
	ctx := context.Background()
	// core-a is alone on its own root while core-b is online: the peer's
	// beacon is not there. On the FIRST pass that is indistinguishable from
	// "core-b booted 10 seconds ago and has not written its beacon yet", so
	// the fault is reported but the routes stay open.
	deps := testDeps(t, t.TempDir(), "core-a", []string{"core-a", "core-b"})
	svc := NewService(deps)

	res := svc.SelfCheck(ctx)

	if res.Status != StatusNotShared {
		t.Fatalf("status = %s, want not-shared", res.Status)
	}
	if !svc.Status().Healthy() {
		t.Fatal("gated on the FIRST not-shared; a Core that just booted would close its peers' routes")
	}
	faults, err := Faults(ctx, deps.Redis)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	if len(faults) != 1 || faults[0].CoreID != "core-a" {
		t.Fatalf("faults = %+v, want one for core-a - it must be visible immediately", faults)
	}
	if len(faults[0].MissingPeers) != 1 || faults[0].MissingPeers[0] != "core-b" {
		t.Errorf("MissingPeers = %v, want [core-b]", faults[0].MissingPeers)
	}
}

func TestSelfCheck_SecondConsecutiveNotSharedGates(t *testing.T) {
	deps := testDeps(t, t.TempDir(), "core-a", []string{"core-a", "core-b"})
	svc := NewService(deps)

	svc.SelfCheck(context.Background())
	svc.SelfCheck(context.Background())

	if svc.Status().Healthy() {
		t.Fatal("still healthy after two consecutive not-shared passes; the fake-shared volume is never gated")
	}
}

func TestSelfCheck_GraceResetsAfterAHealthyPass(t *testing.T) {
	// One bad pass followed by a good one must not leave the Core one tick
	// away from gating for the rest of its life.
	online := []string{"core-a", "core-b"}
	deps := testDeps(t, t.TempDir(), "core-a", online)
	svc := NewService(deps)

	svc.SelfCheck(context.Background()) // not-shared #1
	deps.OnlineCores = func(context.Context) ([]string, error) { return []string{"core-a"}, nil }
	svc.deps = deps
	if got := svc.SelfCheck(context.Background()).Status; got != StatusOK {
		t.Fatalf("status = %s, want ok once core-a is alone", got)
	}

	deps.OnlineCores = func(context.Context) ([]string, error) { return online, nil }
	svc.deps = deps
	svc.SelfCheck(context.Background()) // not-shared #1 again, not #2

	if !svc.Status().Healthy() {
		t.Fatal("gated on a single not-shared after a healthy pass; the counter did not reset")
	}
}

func TestSelfCheck_OtherFailuresGateImmediately(t *testing.T) {
	// Only not-shared is ambiguous. A read-only mount is directly observed
	// and needs no grace.
	root := t.TempDir()
	deps := testDeps(t, root, "core-a", []string{"core-a", "core-b"})
	deps.NewProvider = func(Config) (storage.StorageProvider, error) {
		p := newProbeProvider(root)
		p.writeErrPrefix = "core-a"
		p.writeErr = errRO
		return p, nil
	}
	svc := NewService(deps)

	if got := svc.SelfCheck(context.Background()).Status; got != StatusWriteDenied {
		t.Fatalf("status = %s, want write-denied", got)
	}
	if svc.Status().Healthy() {
		t.Fatal("a read-only mount did not gate on the first pass")
	}
}

func TestSelfCheck_RecoveryClearsTheFaultAndUngates(t *testing.T) {
	ctx := context.Background()
	online := []string{"core-a", "core-b"}
	deps := testDeps(t, t.TempDir(), "core-a", online)
	svc := NewService(deps)

	svc.SelfCheck(ctx)
	svc.SelfCheck(ctx)
	if svc.Status().Healthy() {
		t.Fatal("setup: expected the Core to be gated after two not-shared passes")
	}

	// core-b goes away, so core-a is legitimately alone again.
	deps.OnlineCores = func(context.Context) ([]string, error) { return []string{"core-a"}, nil }
	svc.deps = deps
	if got := svc.SelfCheck(ctx).Status; got != StatusOK {
		t.Fatalf("status = %s, want ok after recovery", got)
	}

	faults, _ := Faults(ctx, deps.Redis)
	if len(faults) != 0 {
		t.Fatalf("faults = %+v, want none after recovery", faults)
	}
	if !svc.Status().Healthy() {
		t.Fatal("still gated after recovery; a transient outage would gate forever")
	}
}

func TestSelfCheck_UnreachableProviderIsAFault(t *testing.T) {
	deps := testDeps(t, t.TempDir(), "core-a", []string{"core-a"})
	deps.NewProvider = func(Config) (storage.StorageProvider, error) {
		return nil, errors.New("mount not present")
	}
	svc := NewService(deps)

	res := svc.SelfCheck(context.Background())

	if res.Status != StatusUnreachable {
		t.Fatalf("status = %s, want unreachable", res.Status)
	}
	if svc.Status().Healthy() {
		t.Fatal("LocalStatus is healthy although the provider will not build")
	}
	faults, _ := Faults(context.Background(), deps.Redis)
	if len(faults) != 1 || faults[0].Detail == "" {
		t.Fatalf("faults = %+v, want one carrying the backend error", faults)
	}
}

func TestSelfCheck_PublishesOnlyOnAStatusChange(t *testing.T) {
	// The panel refreshes on this event. Publishing every 120s tick would
	// make a healthy fleet chatter forever.
	var published []string
	deps := testDeps(t, t.TempDir(), "core-a", []string{"core-a"})
	deps.Publish = func(eventType string, _ map[string]interface{}) {
		published = append(published, eventType)
	}
	svc := NewService(deps)

	svc.SelfCheck(context.Background()) // ok, first observation -> publish
	svc.SelfCheck(context.Background()) // still ok -> silent

	if len(published) != 1 {
		t.Fatalf("published %d events (%v), want exactly 1", len(published), published)
	}
	if published[0] != EventStorageReachChanged {
		t.Errorf("event = %q, want %q", published[0], EventStorageReachChanged)
	}
}

func TestSelfCheck_AddsItselfWithoutWritingIntoTheCallersSlice(t *testing.T) {
	// OnlineCores returns a slice with SPARE CAPACITY, which an implementation
	// is free to reuse between calls. Appending this Core's id to it in place
	// would write into that backing array behind the caller's back.
	backing := make([]string, 1, 4)
	backing[0] = "core-b"
	deps := testDeps(t, t.TempDir(), "core-a", nil)
	deps.OnlineCores = func(context.Context) ([]string, error) { return backing, nil }
	svc := NewService(deps)

	res := svc.SelfCheck(context.Background())

	// core-a's own heartbeat has not landed, so it is missing from the online
	// set and has to add itself. That it gets a verdict AT ALL is the proof it
	// did: Aggregate only emits a result for a listed participant.
	if res.Status != StatusNotShared {
		t.Fatalf("status = %s, want not-shared; a Core absent from its own participant set gets no verdict", res.Status)
	}
	if len(res.MissingPeers) != 1 || res.MissingPeers[0] != "core-b" {
		t.Errorf("MissingPeers = %v, want [core-b]", res.MissingPeers)
	}

	if len(backing) != 1 || backing[0] != "core-b" {
		t.Errorf("the caller's slice was modified: %v", backing)
	}
	if spare := backing[:cap(backing)]; spare[1] != "" {
		t.Fatalf("wrote %q into the caller's spare capacity; an OnlineCores that reuses its buffer would hand out corrupted data", spare[1])
	}
}

func TestSelfCheck_OnlineCoreLookupFailureDoesNotGate(t *testing.T) {
	// Redis being unreachable is separately visible on the health page.
	// Gating every storage route because we could not COUNT peers would turn
	// a Redis blip into a storage outage.
	deps := testDeps(t, t.TempDir(), "core-a", nil)
	deps.OnlineCores = func(context.Context) ([]string, error) { return nil, errors.New("redis down") }
	svc := NewService(deps)

	res := svc.SelfCheck(context.Background())

	if !svc.Status().Healthy() {
		t.Fatalf("gated on a peer-lookup failure (status %s); a Redis blip must not close storage", res.Status)
	}
}

func TestStart_RunsABootCheckAndStopsWithTheContext(t *testing.T) {
	deps := testDeps(t, t.TempDir(), "core-a", []string{"core-a", "core-b"})
	svc := NewService(deps)

	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)

	// The boot check must run immediately, not on the first 120s tick: a Core
	// joining a broken deployment has to report it the moment it joins the
	// load balancer.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if faults, _ := Faults(ctx, deps.Redis); len(faults) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	faults, _ := Faults(ctx, deps.Redis)
	if len(faults) == 0 {
		t.Fatal("the boot self-check never ran; a scaled-up Core would report nothing for 120s")
	}

	cancel()
	select {
	case <-svc.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the service loop did not stop when its context was cancelled")
	}
}

// hangingProvider models a wedged mount: WriteFile blocks until the test
// releases it, and deliberately ignores ctx. That is not laziness in the fake,
// it is the fact the watchdog exists for - a filesystem call already stuck
// inside a syscall cannot be interrupted from userspace, so a context timeout
// alone would leave the service goroutine exactly as stuck.
//
// storage.StorageProvider is embedded nil on purpose: any method other than
// the two below is one this fake never expects to be called, and a nil panic
// says so loudly instead of quietly succeeding.
type hangingProvider struct {
	storage.StorageProvider
	// entered takes one token per WriteFile entry, so a test can both wait for
	// the first check and count how many checks were ever started.
	entered chan struct{}
	release chan struct{}
}

func newHangingProvider() *hangingProvider {
	return &hangingProvider{entered: make(chan struct{}, 64), release: make(chan struct{})}
}

func (p *hangingProvider) WriteFile(context.Context, string, io.Reader) error {
	p.entered <- struct{}{}
	<-p.release
	return errors.New("storage released")
}

// ListFiles is reached once WriteFile finally fails, so it has to answer;
// erroring here keeps the refresh away from the nil embedded provider.
func (p *hangingProvider) ListFiles(context.Context, string) ([]storage.FileInfo, error) {
	return nil, errors.New("storage released")
}

// hungServiceUnderTest wires a service to a provider that never returns, with
// every interval in milliseconds so no test waits on the real 15s budget or
// the real 120s period.
func hungServiceUnderTest(t *testing.T, selfCheckEvery, budget time.Duration) (*Service, *hangingProvider, ServiceDeps, context.CancelFunc) {
	t.Helper()
	prov := newHangingProvider()
	deps := testDeps(t, t.TempDir(), "core-a", []string{"core-a"})
	deps.NewProvider = func(Config) (storage.StorageProvider, error) { return prov, nil }

	svc := NewService(deps)
	svc.pollEvery = 5 * time.Millisecond
	svc.selfCheckEvery = selfCheckEvery
	svc.selfCheckBudget = budget

	ctx, cancel := context.WithCancel(context.Background())
	// The provider ignores ctx by design, so cancelling is not enough to end
	// the orphaned check goroutine; release it explicitly or it outlives the
	// test.
	t.Cleanup(func() { cancel(); close(prov.release) })
	svc.Start(ctx)
	return svc, prov, deps, cancel
}

// awaitFirstCheck blocks until the boot self-check has reached the provider.
func awaitFirstCheck(t *testing.T, prov *hangingProvider) {
	t.Helper()
	select {
	case <-prov.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the boot self-check never reached the storage provider")
	}
}

// waitUntil polls cond until it holds. Every caller drives the loop at
// millisecond intervals, so the 2s cap is a failure detector, not a wait.
func waitUntil(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestStart_AHungStorageBackendDoesNotStallTheServiceLoop(t *testing.T) {
	// The failure being guarded: with the check called synchronously, a stale
	// NFS mount blocks the write, run() never reaches its select, ctx.Done() is
	// never observed, Done() never closes, and round participation stops dead.
	svc, prov, _, cancel := hungServiceUnderTest(t, 10*time.Second, 30*time.Millisecond)
	awaitFirstCheck(t, prov)

	cancel()
	select {
	case <-svc.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the service loop did not stop while a storage write was hung; a wedged mount would freeze it forever")
	}
}

func TestStart_ACheckThatOverrunsItsBudgetGatesAndRecordsAFault(t *testing.T) {
	// This is the bug itself, not just the symptom: while the check hangs,
	// nothing ever updates LocalStatus, and LocalStatus starts HEALTHY by
	// design - so the Core goes on claiming access it cannot prove.
	svc, prov, deps, _ := hungServiceUnderTest(t, 10*time.Second, 30*time.Millisecond)
	awaitFirstCheck(t, prov)

	waitUntil(t, "still healthy after the self-check blew its budget; a Core wedged on a dead mount must stop vouching for it", func() bool {
		return !svc.Status().Healthy()
	})
	if status, detail := svc.Status().Snapshot(); status != StatusUnreachable {
		t.Fatalf("LocalStatus = %s, want unreachable", status)
	} else if detail == "" {
		t.Error("Detail is empty; the operator is never told the check timed out")
	}

	var faults []Fault
	waitUntil(t, "no fault recorded for a timed-out self-check; the panel would show a healthy fleet", func() bool {
		faults, _ = Faults(context.Background(), deps.Redis)
		return len(faults) > 0
	})
	if len(faults) != 1 || faults[0].CoreID != "core-a" || faults[0].Status != StatusUnreachable {
		t.Fatalf("faults = %+v, want exactly one unreachable fault for core-a", faults)
	}
}

func TestStart_DoesNotStartASecondCheckWhileTheFirstIsStillBlocked(t *testing.T) {
	// A loop that re-armed when the BUDGET expired rather than when the check
	// actually returned would strand one more goroutine on the dead mount every
	// interval, forever.
	const selfCheckEvery = 15 * time.Millisecond
	svc, prov, _, _ := hungServiceUnderTest(t, selfCheckEvery, 10*time.Millisecond)
	awaitFirstCheck(t, prov)

	// The budget is shorter than the interval, so by now the first check has
	// already been ruled on and every one of these ten intervals was a chance
	// to start another.
	waitUntil(t, "the first check never blew its budget", func() bool { return !svc.Status().Healthy() })
	time.Sleep(10 * selfCheckEvery)

	extra := 0
drain:
	for {
		select {
		case <-prov.entered:
			extra++
		default:
			break drain
		}
	}
	if extra != 0 {
		t.Fatalf("%d further self-check(s) started while the first was still blocked; a hung mount would leak a goroutine per interval", extra)
	}
}

// publishPendingRoundFor stages a config round for participantID as if some
// OTHER Core were coordinating it, so PendingRoundID and RunParticipant see a
// real round in flight without a second live service. RunRound itself is not
// used here: it blocks synchronously for the whole round, but this test needs
// a round already sitting in Redis WHILE the service loop is running, not a
// coordinator call for the test to wait out.
func publishPendingRoundFor(t *testing.T, rdb *redis.Client, participantID, coordinatorID string) {
	t.Helper()
	cfg := Config{Backend: "path", Path: "/mnt/shared"}
	meta := roundMeta{
		RoundID:      "round-under-test",
		Fingerprint:  Fingerprint(cfg),
		Participants: []string{participantID},
		Coordinator:  coordinatorID,
		// Comfortably longer than this test runs: the hang happens inside the
		// first WriteFile call, before RunParticipant's own deadline logic ever
		// gets a chance to matter.
		DeadlineUnixMilli: time.Now().Add(2 * time.Second).UnixMilli(),
		PollEveryMillis:   50,
	}
	if err := publishRound(context.Background(), rdb, meta, cfg); err != nil {
		t.Fatalf("publishRound: %v", err)
	}
}

func TestStart_AHungStorageBackendDoesNotStallRoundParticipation(t *testing.T) {
	// Same hazard as TestStart_AHungStorageBackendDoesNotStallTheServiceLoop,
	// one tick later: RunParticipant writes through the exact same provider, so
	// a wedged mount can wedge the loop via round participation instead of the
	// self-check, which already has its own watchdog (see run()'s doc comment
	// for why the leaked goroutine is the accepted half of that trade).
	prov := newHangingProvider()
	deps := testDeps(t, t.TempDir(), "core-a", []string{"core-a"})
	deps.NewProvider = func(Config) (storage.StorageProvider, error) { return prov, nil }

	svc := NewService(deps)
	svc.pollEvery = 5 * time.Millisecond
	// Long enough that the self-check never refires mid-test. Its own hang on
	// the same provider is harmless here: that watchdog is unchanged and is
	// already proven by the tests above, so it must not be what this test
	// depends on.
	svc.selfCheckEvery = 10 * time.Second
	svc.selfCheckBudget = 10 * time.Second
	svc.roundBudget = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	// The provider ignores ctx by design, so cancelling is not enough to end
	// either orphaned goroutine; release it explicitly or it outlives the test.
	t.Cleanup(func() { cancel(); close(prov.release) })
	svc.Start(ctx)

	awaitFirstCheck(t, prov) // the boot self-check reaches the provider and hangs

	publishPendingRoundFor(t, deps.Redis, "core-a", "core-other")

	// Round participation now also reaches the same hung provider: a second
	// entry into the same channel, from a second goroutine.
	select {
	case <-prov.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("round participation never reached the storage provider")
	}

	cancel()
	select {
	case <-svc.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the service loop did not stop while round participation was hung; a wedged mount would freeze round answering forever")
	}
}
