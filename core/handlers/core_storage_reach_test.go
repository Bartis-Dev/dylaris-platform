package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"dylaris-core/services/storagereach"
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
