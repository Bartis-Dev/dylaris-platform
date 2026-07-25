package storagereach

import (
	"context"
	"sync"
	"testing"
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

func TestLocalStatus_StartsHealthy(t *testing.T) {
	// A Core that has not run its check yet must not gate its own routes:
	// the boot check runs a moment later and will gate if it needs to.
	ls := NewLocalStatus()
	if !ls.Healthy() {
		t.Fatal("a fresh LocalStatus is not healthy; every Core would 503 at boot")
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
