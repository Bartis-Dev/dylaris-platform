package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"dylaris-core/services"
	"dylaris-core/services/storagereach"
	"dylaris-core/storage"
)

// defaultReachRoundDeadline is the owner-chosen cap for a config round:
// snappy beats worst-case, and a genuinely shared but slow mount that misses
// it is not a permanent failure - Retry re-runs the round.
const defaultReachRoundDeadline = 15 * time.Second

// reachRoundLockKey and reachRoundLockTTL guard against two config rounds
// running at once, cluster-wide.
//
// RunRound's own discovery-key cleanup (round.go) deletes the single global
// "current round" key unconditionally, without checking that the key still
// points at ITS round. Two rounds in flight together - two admins saving near
// the same moment, or one admin's retry landing on a different Core replica
// behind the load balancer while the first attempt is still running - would
// let one coordinator delete the other's still-open round out from under it,
// turning a real, slow-but-honest peer into a false no-response. This lock
// stops a second round from starting anywhere in the fleet while one runs.
//
// Matches the SetNX-with-TTL cluster lock convention already used by
// RoutingMigrationService.Run, StorageMigrationService.Start and friends.
const (
	reachRoundLockKey = "dylaris:storagereach:save-lock"
	// reachRoundLockTTL outlives the round's own hard cap (15s) with margin, so
	// a Core that dies mid-round - before its deferred release ever runs -
	// does not wedge every future save behind a lock nobody will clear.
	reachRoundLockTTL = 20 * time.Second
)

// sharedStorageRefusal is returned when a config must not be persisted because
// the fleet could not prove shared access. It carries the whole RoundResult,
// not just a message: the panel names the failing Core and its taxonomy
// reason, which is the difference between "it broke" and "fix host-2's mount".
type sharedStorageRefusal struct {
	status  int
	message string
	result  *storagereach.RoundResult
}

func (e *sharedStorageRefusal) Error() string { return e.message }

// CoreStorageToReachConfig narrows the persisted settings shape to what the
// verifier needs. The s3 secret travels because each participant builds a real
// client from it; without it every peer would report unreachable.
//
// Exported (rather than left package-private) because main.go's boot wiring
// builds storagereach.ServiceDeps.ConfigFor from it, and that package cannot
// import handlers.
func CoreStorageToReachConfig(cfg CoreStorageConfig) storagereach.Config {
	return storagereach.Config{
		Backend:     cfg.Backend,
		Path:        cfg.Path,
		S3Endpoint:  cfg.S3Endpoint,
		S3Bucket:    cfg.S3Bucket,
		S3Region:    cfg.S3Region,
		S3AccessKey: cfg.S3AccessKey,
		S3SecretKey: cfg.S3SecretKey,
		S3PathStyle: cfg.S3PathStyle,
		S3Prefix:    cfg.S3Prefix,
	}
}

// NewReachProvider builds a provider rooted at the backend ROOT rather than a
// sub-prefix, so probe files land in .dylaris-reachability/ beside library/
// and ticket-attachments/ instead of inside one of them.
//
// Exported for the same reason as CoreStorageToReachConfig: main.go wires this
// in directly as storagereach.ServiceDeps.NewProvider.
func (s *AppState) NewReachProvider(cfg storagereach.Config) (storage.StorageProvider, error) {
	return newStorageProviderForConfig(CoreStorageConfig{
		Backend:     cfg.Backend,
		Path:        cfg.Path,
		S3Endpoint:  cfg.S3Endpoint,
		S3Bucket:    cfg.S3Bucket,
		S3Region:    cfg.S3Region,
		S3AccessKey: cfg.S3AccessKey,
		S3SecretKey: cfg.S3SecretKey,
		S3PathStyle: cfg.S3PathStyle,
		S3Prefix:    cfg.S3Prefix,
	}, "", s.StorageGate, s.StorageS3)
}

// EffectiveCoreStorageConfig exports effectiveCoreStorageConfig for callers
// outside this package.
//
// main.go's storagereach ConfigFor needs the EFFECTIVE config, not
// LoadCoreStorageConfig's raw persisted one: a deployment that selects a
// storage connection stores its actual s3 credentials on the connection row,
// not in the inline core_storage_s3_* settings, so LoadCoreStorageConfig alone
// would hand the verifier an empty inline config and every Core would report
// unreachable for a perfectly working, connection-backed setup.
func (s *AppState) EffectiveCoreStorageConfig() (CoreStorageConfig, error) {
	return s.effectiveCoreStorageConfig()
}

// checkSharedStorageReachable replaces the count-only host-path guard with an
// actual proof. It runs a verifier round across every online Core and refuses
// the save unless all of them wrote, read each other, and cross-wrote.
//
// It covers s3 as well as host paths: the old guard ignored s3 entirely, but a
// Core holding credentials that do not work is exactly as broken as a Core on
// an unshared mount, and just as silent.
//
// A verification that could not run is a refusal, not a pass. This is a rare,
// deliberate admin action, so "could not verify, try again" is cheap; letting
// it through would silently split file storage across instances, which is the
// failure this exists to prevent.
func (s *AppState) checkSharedStorageReachable(ctx context.Context, cfg CoreStorageConfig) error {
	online, err := services.OnlineCoreIDs(ctx, s.Redis)
	if err != nil {
		log.Printf("core storage: could not list online Cores: %v", err)
		return &sharedStorageRefusal{
			status:  http.StatusServiceUnavailable,
			message: "Could not verify which Core instances are online, so the storage settings cannot be saved right now. Check that Redis is reachable and try again.",
		}
	}
	// 0 and 1 are both "not more than one": a count of 0 means this Core's own
	// heartbeat has not landed yet, not that no Core is running. One Core
	// cannot fail a sharing check, so there is nothing to prove.
	if len(online) <= 1 {
		return nil
	}
	if s.StorageReach == nil {
		return &sharedStorageRefusal{
			status:  http.StatusServiceUnavailable,
			message: "The storage reachability verifier is not running on this Core, so a multi-Core storage change cannot be verified.",
		}
	}

	// Local mutex (single-process safety) plus a Redis SETNX lock
	// (cluster-wide safety) - the same two-layer pattern
	// RoutingMigrationService.Run uses. The mutex alone would not stop two
	// different Core replicas from both starting a round for two concurrent
	// save requests; the Redis lock is what makes "no concurrent rounds" true
	// fleet-wide, not just on this process.
	s.reachRoundMu.Lock()
	defer s.reachRoundMu.Unlock()

	acquired, lockErr := s.Redis.SetNX(ctx, reachRoundLockKey, "1", reachRoundLockTTL).Result()
	if lockErr != nil {
		log.Printf("core storage: could not acquire the config-round lock: %v", lockErr)
		return &sharedStorageRefusal{
			status:  http.StatusServiceUnavailable,
			message: "Could not verify shared storage access right now. Check that Redis is reachable and try again.",
		}
	}
	if !acquired {
		return &sharedStorageRefusal{
			status:  http.StatusConflict,
			message: "Another storage configuration change is currently being verified on this deployment. Wait for it to finish and try again.",
		}
	}
	defer func() {
		if err := s.Redis.Del(context.WithoutCancel(ctx), reachRoundLockKey).Err(); err != nil {
			log.Printf("core storage: could not release the config-round lock: %v", err)
		}
	}()

	deadline := s.reachRoundDeadline
	if deadline <= 0 {
		deadline = defaultReachRoundDeadline
	}

	reachCfg := CoreStorageToReachConfig(cfg)
	result, err := s.StorageReach.Coordinator().RunRound(ctx, reachCfg, online, storagereach.RoundOptions{
		Deadline: deadline,
		// Each partial result becomes the panel's live "X/N cores confirmed"
		// counter. Without this the admin stares at a spinner for 15s.
		OnProgress: func(r storagereach.RoundResult) {
			s.Events.Publish(ctx, storagereach.EventStorageReachChanged, map[string]interface{}{
				"round":     r.RoundID,
				"confirmed": r.Confirmed,
				"total":     r.Total,
				"done":      r.Done,
				"ok":        r.OK,
				"results":   r.Results,
			})
		},
	})
	if err != nil {
		return &sharedStorageRefusal{
			status:  http.StatusServiceUnavailable,
			message: fmt.Sprintf("Could not verify shared storage access across %d Cores: %v", len(online), err),
			result:  &result,
		}
	}
	if !result.OK {
		return &sharedStorageRefusal{
			status: http.StatusConflict,
			message: fmt.Sprintf("Only %d of %d Cores could prove they can read and write this storage. The settings were not saved.",
				result.Confirmed, result.Total),
			result: &result,
		}
	}
	return nil
}

// sendReachRefusal writes a refusal as JSON, including the per-Core round
// result when there is one so the panel can name the failing Cores.
func (h *CoreStorageHandler) sendReachRefusal(w http.ResponseWriter, err error) {
	var refusal *sharedStorageRefusal
	if !errors.As(err, &refusal) {
		sendJSONError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(refusal.status)
	body := map[string]interface{}{"success": false, "message": refusal.message}
	if refusal.result != nil {
		body["round"] = refusal.result
	}
	_ = json.NewEncoder(w).Encode(body)
}

// StorageReachStatus GET /api/settings/storage-reach - PANEL settings.read.
// The fleet storage-health surface: which Cores are currently failing their
// self-check, and which are online at all.
//
// READ-ONLY by design: it must never trigger a check itself. Service.SelfCheck
// is documented as unsafe to call concurrently with the service's own running
// Start loop - both paths end in apply, whose state (notSharedStreak,
// lastStatus) is single-writer by convention and not mutex-guarded - so this
// handler only reads what the periodic loop and any in-flight config round
// have already recorded in Redis.
func (h *CoreStorageHandler) StorageReachStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	faults, err := storagereach.Faults(ctx, h.state.Redis)
	if err != nil {
		sendJSONError(w, "Could not read storage health", http.StatusServiceUnavailable)
		return
	}
	online, err := services.OnlineCoreIDs(ctx, h.state.Redis)
	if err != nil {
		sendJSONError(w, "Could not read the online Core set", http.StatusServiceUnavailable)
		return
	}
	if faults == nil {
		// [] rather than null: the panel maps over this directly.
		faults = []storagereach.Fault{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"faults":      faults,
		"onlineCores": online,
	})
}
