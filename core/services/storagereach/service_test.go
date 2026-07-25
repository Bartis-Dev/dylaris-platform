package storagereach

import (
	"context"
	"errors"
	"testing"
	"time"

	"dylaris-core/storage"
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
