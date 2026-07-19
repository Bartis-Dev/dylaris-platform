package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"dylaris-core/models"
	"dylaris-core/services/storagemigrate"

	"github.com/redis/go-redis/v9"
)

// StorageMigrationPhase tracks where a storage migration job is in its
// lifecycle. Sequences:
//
//	migrate:  preparing -> manifesting -> copying -> verifying -> switching_config -> [deleting_source] -> done
//	manifest: preparing -> manifesting -> done
//	verify:   preparing -> verifying -> done
//
// failed and cancelled are terminal from any phase.
//
// The migrate order is a SAFETY REQUIREMENT, not a convention:
//   - switching_config runs only after a verification that passed
//     (AuthorizeConfigSwitch).
//   - deleting_source runs only after switching_config succeeded
//     (AuthorizeSourceDelete takes that outcome as an argument). Deleting first
//     would leave the live config pointing at deleted data for the width of
//     that window, so the ordering is enforced rather than merely documented.
//   - a failed switch fails the job having deleted NOTHING, and says so.
//
// switching_config and deleting_source are both non-cancellable: a half-applied
// config switch is worse than a completed one, exactly as for a half-cancelled
// delete.
type StorageMigrationPhase string

const (
	StoragePhasePreparing       StorageMigrationPhase = "preparing"
	StoragePhaseManifesting     StorageMigrationPhase = "manifesting"
	StoragePhaseCopying         StorageMigrationPhase = "copying"
	StoragePhaseVerifying       StorageMigrationPhase = "verifying"
	StoragePhaseSwitchingConfig StorageMigrationPhase = "switching_config"
	StoragePhaseDeleting        StorageMigrationPhase = "deleting_source"
	StoragePhaseDone            StorageMigrationPhase = "done"
	StoragePhaseFailed          StorageMigrationPhase = "failed"
	StoragePhaseCancelled       StorageMigrationPhase = "cancelled"
)

// StorageJobKind selects which phase sequence runs.
type StorageJobKind string

const (
	StorageJobMigrate  StorageJobKind = "migrate"  // manifest -> copy -> verify -> [delete]
	StorageJobManifest StorageJobKind = "manifest" // manifest only (manual mode, step 1)
	StorageJobVerify   StorageJobKind = "verify"   // verify a target against a stored manifest
)

// Redis keys and timings. Mirrors services/db_migration_job.go, with a longer
// lock TTL because a single large object can take minutes to stream.
const (
	storageMigrationJobKey    = "dylaris:storagemigration:job"
	storageMigrationLockKey   = "dylaris:storagemigration:lock"
	storageMigrationCancelKey = "dylaris:storagemigration:cancel"
	storageMigrationLockTTL   = 5 * time.Minute
	storageHeartbeat          = 20 * time.Second
	storageStaleAfter         = 75 * time.Second // > heartbeat, so a live job never looks stale
	storageMaxLogLines        = 500
	storageJobTTL             = 7 * 24 * time.Hour
)

// ErrStorageMigrationRunning is returned when a job is already active
// cluster-wide. The HTTP layer maps it to 409.
var ErrStorageMigrationRunning = errors.New("a storage migration is already running")

// ErrNoStorageMigrationJob is returned by Cancel when there is nothing to
// cancel.
var ErrNoStorageMigrationJob = errors.New("no storage migration job to cancel")

// StorageMigrationJob is the shared, Redis-backed record every admin can poll.
type StorageMigrationJob struct {
	ID            string                `json:"id"`
	Kind          StorageJobKind        `json:"kind"`
	Phase         StorageMigrationPhase `json:"phase"`
	DataSet       string                `json:"dataSet"`
	SourceLabel   string                `json:"sourceLabel"`
	TargetLabel   string                `json:"targetLabel"`
	StartedBy     string                `json:"startedBy"`
	StartedByName string                `json:"startedByName"`
	StartedAt     time.Time             `json:"startedAt"`
	UpdatedAt     time.Time             `json:"updatedAt"`
	FinishedAt    *time.Time            `json:"finishedAt,omitempty"`

	ObjectsTotal   int64  `json:"objectsTotal"`
	ObjectsDone    int64  `json:"objectsDone"`
	ObjectsSkipped int64  `json:"objectsSkipped"` // identical by checksum, not re-copied
	BytesTotal     int64  `json:"bytesTotal"`
	BytesDone      int64  `json:"bytesDone"`
	CurrentKey     string `json:"currentKey"`

	ManifestID   int  `json:"manifestId,omitempty"`
	DeleteSource bool `json:"deleteSource"`
	// ConfigSwitched reports whether the switching_config phase actually
	// repointed a Core-managed config at the target. It is false for a
	// data-set-to-data-set migrate, where there is no such config, and false
	// after a switch that failed.
	//
	// NOTE what is deliberately ABSENT from this struct: the ad-hoc
	// StorageTargetConfig. It may hold a live S3 secret, and this struct is
	// marshalled into Redis with a 7-day TTL and served to every holder of
	// settings.read. The runner keeps the config in a local variable and puts
	// only the credential-free TargetLabel here. Adding a target-config field
	// would turn a request-scoped credential into a durable, over-shared one.
	ConfigSwitched bool                                `json:"configSwitched"`
	VerifyMode     string                              `json:"verifyMode"` // "full" | "sample"
	Verify         *storagemigrate.StorageVerifyReport `json:"verify,omitempty"`

	Log   []string `json:"log"`
	Error string   `json:"error,omitempty"`
	Stale bool     `json:"stale"` // computed on read, not persisted authoritatively
}

func (j *StorageMigrationJob) appendLog(line string) {
	j.Log = append(j.Log, line)
	if len(j.Log) > storageMaxLogLines {
		j.Log = j.Log[len(j.Log)-storageMaxLogLines:]
	}
}

// StorageMigrationInProgress reports whether a phase is still live. Mirrored
// exactly by storageMigrationInProgress in the panel.
func StorageMigrationInProgress(p StorageMigrationPhase) bool {
	switch p {
	case StoragePhasePreparing, StoragePhaseManifesting, StoragePhaseCopying,
		StoragePhaseVerifying, StoragePhaseSwitchingConfig, StoragePhaseDeleting:
		return true
	}
	return false
}

// StorageTargetConfig is an AD-HOC target storage config supplied in a start
// request and NOT read from the saved settings.
//
// This is what makes the three Core-file-storage data sets (library,
// ticket-attachments, ticket-backups) and modpacks migratable at all: their
// saved config describes where they live NOW, so using it as the target would
// make source == target. The operator instead names the destination inline -
// another S3, a WebDAV/NFS-mounted path, or back - and the engine builds a
// provider from it. The mechanism already exists and is proven: TestConnection
// (handlers/core_storage.go) builds a working provider from an unsaved
// candidate config via newStorageProviderForConfig.
//
// Same wire shape as handlers.CoreStorageConfig. It is declared HERE rather
// than there because both StorageMigrationRequest and StorageDataSetResolver
// need it, and services cannot import handlers (handlers imports services).
// handlers converts between the two in ONE place (Task 14).
//
// S3SecretKey is a live credential. It exists in exactly two places: this
// request-scoped struct, for the life of the run, and - only after a successful
// switch - the normal core-storage settings write path. It is never logged,
// never echoed in a response, and never stored on StorageMigrationJob.
type StorageTargetConfig struct {
	Backend       string `json:"backend"` // "path" | "s3"
	Path          string `json:"path"`
	PathConfirmed bool   `json:"pathConfirmed"`
	S3Endpoint    string `json:"s3Endpoint"`
	S3Bucket      string `json:"s3Bucket"`
	S3Region      string `json:"s3Region"`
	S3AccessKey   string `json:"s3AccessKey"`
	S3SecretKey   string `json:"s3SecretKey,omitempty"`
	S3PathStyle   bool   `json:"s3PathStyle"`
	S3Prefix      string `json:"s3Prefix"`
}

// StorageMigrationRequest is the validated shape of a start request.
type StorageMigrationRequest struct {
	Kind    StorageJobKind `json:"kind"`
	DataSet string         `json:"dataSet"`
	// TargetDataSet and TargetConfig are mutually exclusive ways to name a
	// migrate target. Exactly one must be set.
	TargetDataSet string               `json:"targetDataSet"`
	TargetConfig  *StorageTargetConfig `json:"targetConfig,omitempty"`
	VerifyMode    string               `json:"verifyMode"`
	DeleteSource  bool                 `json:"deleteSource"`
	ManifestID    int                  `json:"manifestId"`
}

// Validate enforces every request-shape rule INCLUDING safety invariant 2, so
// the rule lives at the API boundary and not only in the UI.
func (r StorageMigrationRequest) Validate() error {
	if r.DataSet == "" {
		return errors.New("dataSet is required")
	}
	switch r.Kind {
	case StorageJobManifest:
		if r.DeleteSource {
			return errors.New("deleteSource is meaningless for a manifest-only job")
		}
		if r.TargetConfig != nil {
			return errors.New("targetConfig is meaningless for a manifest-only job")
		}
		return nil
	case StorageJobVerify:
		if r.DeleteSource {
			return errors.New("deleteSource is meaningless for a verify-only job")
		}
		if r.TargetConfig != nil {
			return errors.New("targetConfig is meaningless for a verify-only job: a verify compares the data set's CURRENT backend against a stored manifest")
		}
		if !storagemigrate.ValidVerifyMode(r.VerifyMode) {
			return fmt.Errorf("verifyMode must be %q or %q", storagemigrate.VerifyModeFull, storagemigrate.VerifyModeSample)
		}
		if r.ManifestID <= 0 {
			return errors.New("manifestId is required for a verify job")
		}
		return nil
	case StorageJobMigrate:
		// Exactly one way to name the target. Both is ambiguous (which wins?),
		// neither is incomplete; refuse rather than guess.
		hasCfg := r.TargetConfig != nil
		hasSet := r.TargetDataSet != ""
		if hasCfg && hasSet {
			return errors.New("name either targetDataSet or targetConfig, not both")
		}
		if !hasCfg && !hasSet {
			return errors.New("a migrate job needs a target: either targetDataSet (another data set) or targetConfig (an ad-hoc storage config)")
		}
		if hasCfg && r.TargetConfig.Backend == "" {
			// Shape check only. The full rules live in
			// handlers.validateCoreStorageConfig, which the resolver applies
			// before a single object is read; duplicating them here would give
			// them two homes and let them drift.
			return errors.New("targetConfig.backend is required (\"path\" or \"s3\")")
		}
		if !storagemigrate.ValidVerifyMode(r.VerifyMode) {
			return fmt.Errorf("verifyMode must be %q or %q", storagemigrate.VerifyModeFull, storagemigrate.VerifyModeSample)
		}
		if r.DeleteSource && r.VerifyMode != storagemigrate.VerifyModeFull {
			// Safety invariant 2 at the boundary: a sampled verification did
			// not check every object, so it can never authorize a delete.
			return errors.New("deleteSource requires verifyMode \"full\": a sampled verification cannot authorize deleting the source")
		}
		if r.DeleteSource && !hasCfg {
			// The engine can only settle the active config when it is the thing
			// doing the repointing, which is the targetConfig form. For a
			// data-set-to-data-set pairing the operator repoints the consuming
			// subsystem afterwards, so a delete here would run BEFORE that
			// repoint and orphan live references. The operator migrates,
			// verifies, repoints, and then removes the old storage themselves.
			return errors.New("deleteSource requires targetConfig: with a targetDataSet target nothing repoints the consuming subsystem, so deleting the source here would orphan live references - migrate, verify, repoint, then remove the old storage yourself")
		}
		return nil
	default:
		return fmt.Errorf("unknown kind %q (want migrate, manifest or verify)", r.Kind)
	}
}

// StorageDataSetInfo describes one migratable data set for the overview.
type StorageDataSetInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// BackendLabel is a HUMAN description of where this data set currently
	// lives. It MUST NOT contain credentials.
	BackendLabel string `json:"backendLabel"`
	Migratable   bool   `json:"migratable"`
	// SupportsTargetConfig reports how this data set names a migrate target.
	// True: it is backed by a single settings-configured backend, so the target
	// is an ad-hoc StorageTargetConfig (library, ticket-attachments,
	// ticket-backups, modpacks). False: the target is another data-set id
	// (server-backups rows, which are already multi-storage by design).
	SupportsTargetConfig bool `json:"supportsTargetConfig"`
	// Note explains a limitation, e.g. that node-local backups cannot be
	// migrated through this engine, or that modpack verification covers
	// database-referenced objects only.
	Note string `json:"note,omitempty"`
}

// StorageDataSetResolver turns a data-set id into a live DataSet plus its
// credential-free backend label, resolves an ad-hoc target config into a live
// DataSet, and owns the config switch. Implemented in handlers (which owns the
// Core file storage config, the modpack settings and the backup_storages
// rows), so this package stays free of them.
type StorageDataSetResolver interface {
	List(ctx context.Context) ([]StorageDataSetInfo, error)
	Resolve(ctx context.Context, id string) (storagemigrate.DataSet, string, error)
	// ResolveTarget builds the target DataSet from an ad-hoc config, scoped to
	// sourceID's sub-prefix. It MUST validate the config and MUST refuse a
	// target that resolves to the same location as the source, so that refusal
	// happens at Start, before a single object is read - never mid-copy, by
	// which point the engine would have overwritten source objects with
	// themselves. Returns a credential-free label.
	ResolveTarget(ctx context.Context, sourceID string, cfg StorageTargetConfig) (storagemigrate.DataSet, string, error)
	// SwitchConfig persists cfg as the ACTIVE config for sourceID, through the
	// same write path the settings form uses. Called only from the
	// switching_config phase, only after a passing verification, and it is the
	// one and only place cfg.S3SecretKey is allowed to be stored.
	SwitchConfig(ctx context.Context, sourceID string, cfg StorageTargetConfig) error
}

// StorageMigrationService orchestrates a single, cluster-wide-exclusive
// storage migration job. The job state lives in Redis so any Core can serve
// the status; a SetNX lock guarantees only one runs at a time.
type StorageMigrationService struct {
	redis    *redis.Client
	store    storagemigrate.ManifestStore
	resolver StorageDataSetResolver

	mu  sync.Mutex
	job *StorageMigrationJob // in-memory copy for the job running on THIS Core
}

// NewStorageMigrationService wires the service.
func NewStorageMigrationService(rdb *redis.Client, st storagemigrate.ManifestStore, resolver StorageDataSetResolver) *StorageMigrationService {
	return &StorageMigrationService{redis: rdb, store: st, resolver: resolver}
}

// Start validates the request, takes the cluster-wide lock, and launches the
// run on this Core. Returns the initial job record.
func (s *StorageMigrationService) Start(ctx context.Context, req StorageMigrationRequest, startedBy, startedByName string) (*StorageMigrationJob, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	src, srcLabel, err := s.resolver.Resolve(ctx, req.DataSet)
	if err != nil {
		return nil, fmt.Errorf("source data set: %w", err)
	}
	var (
		target      storagemigrate.DataSet
		targetLabel string
	)
	if req.Kind == StorageJobMigrate {
		if req.TargetConfig != nil {
			// Ad-hoc target. ResolveTarget validates the config AND refuses a
			// same-location target, so both failures land here - at Start,
			// before the lock is taken and before a single object is read.
			target, targetLabel, err = s.resolver.ResolveTarget(ctx, req.DataSet, *req.TargetConfig)
			if err != nil {
				return nil, fmt.Errorf("target storage: %w", err)
			}
		} else {
			target, targetLabel, err = s.resolver.Resolve(ctx, req.TargetDataSet)
			if err != nil {
				return nil, fmt.Errorf("target data set: %w", err)
			}
			if req.DataSet == req.TargetDataSet {
				return nil, errors.New("the target is the same data set as the source")
			}
		}
	}
	if req.Kind == StorageJobVerify {
		// Verify compares the CURRENT state of the named data set against a
		// stored manifest, so the "source" IS the target here.
		target, targetLabel = src, srcLabel
	}

	jobID := newJobID()
	ok, err := s.redis.SetNX(ctx, storageMigrationLockKey, jobID, storageMigrationLockTTL).Result()
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	if !ok {
		return nil, ErrStorageMigrationRunning
	}
	// A stale cancel flag from a previous job must never cancel this one.
	s.redis.Del(ctx, storageMigrationCancelKey)

	now := time.Now()
	job := &StorageMigrationJob{
		ID:            jobID,
		Kind:          req.Kind,
		Phase:         StoragePhasePreparing,
		DataSet:       req.DataSet,
		SourceLabel:   srcLabel,
		TargetLabel:   targetLabel,
		StartedBy:     startedBy,
		StartedByName: startedByName,
		StartedAt:     now,
		UpdatedAt:     now,
		DeleteSource:  req.DeleteSource,
		VerifyMode:    req.VerifyMode,
		Log:           []string{},
	}
	job.appendLog(fmt.Sprintf("%s job started by %s.", req.Kind, displayName(startedByName, startedBy)))
	job.appendLog(fmt.Sprintf("Source: %s (%s).", req.DataSet, srcLabel))
	if targetLabel != "" {
		// targetLabel comes from the resolver and is credential-free by
		// contract. req.TargetConfig is NEVER logged: it may hold a live S3
		// secret.
		job.appendLog(fmt.Sprintf("Target: %s.", targetLabel))
	}
	if req.Kind == StorageJobMigrate {
		job.appendLog("Order: copy, verify, switch the active config to the target, then delete the old copy if opted in. The switch happens only after a passing verification, and nothing is deleted before the switch succeeds.")
	}
	if req.DeleteSource {
		job.appendLog("The source WILL be deleted, but only after a full verification passes AND the config switch succeeds.")
	}

	s.mu.Lock()
	s.job = job
	s.persistLocked(job)
	s.mu.Unlock()

	go s.run(req, src, target)
	return job, nil
}

// GetJob reads the shared job record from Redis (any Core can answer). The
// boolean is false when no job has ever run.
func (s *StorageMigrationService) GetJob(ctx context.Context) (*StorageMigrationJob, bool, error) {
	raw, err := s.redis.Get(ctx, storageMigrationJobKey).Result()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var job StorageMigrationJob
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		return nil, false, err
	}
	// An in-progress job whose heartbeat stopped (e.g. the Core running it
	// died) is flagged so admins are not misled into waiting forever.
	if StorageMigrationInProgress(job.Phase) && time.Since(job.UpdatedAt) > storageStaleAfter {
		job.Stale = true
	}
	return &job, true, nil
}

// Cancel asks the running job to unwind. The flag lives in Redis, so any Core
// can cancel a job running on any other Core. The runner checks it at every
// OBJECT boundary and on the heartbeat tick, never mid-object.
//
// deleting_source is deliberately NOT cancellable: by then verification has
// passed and a half-cancelled delete is strictly worse than a completed one.
func (s *StorageMigrationService) Cancel(ctx context.Context) error {
	job, ok, err := s.GetJob(ctx)
	if err != nil {
		return err
	}
	if !ok || !StorageMigrationInProgress(job.Phase) {
		return ErrNoStorageMigrationJob
	}
	if job.Phase == StoragePhaseSwitchingConfig {
		return errors.New("the config-switch phase cannot be cancelled; a half-applied switch is worse than a completed one")
	}
	if job.Phase == StoragePhaseDeleting {
		return errors.New("the delete-source phase cannot be cancelled; it is already the point of no return")
	}
	// storageJobTTL, not the lock TTL: the heartbeat refreshes the lock but not
	// this key, so a lock-length TTL could let a cancel issued during a long
	// single-object copy expire before the next object boundary is reached -
	// the operator would see "cancelling" and the job would finish anyway. The
	// key outliving its job is harmless: the runner deletes it when it exits,
	// and Start deletes it again before launching the next one.
	return s.redis.Set(ctx, storageMigrationCancelKey, job.ID, storageJobTTL).Err()
}

// cancelled reports whether a cancel has been requested for THIS job.
func (s *StorageMigrationService) cancelled(jobID string) bool {
	v, err := s.redis.Get(context.Background(), storageMigrationCancelKey).Result()
	return err == nil && v == jobID
}

func (s *StorageMigrationService) run(req StorageMigrationRequest, src, target storagemigrate.DataSet) {
	ctx := context.Background()
	jobID := s.jobID()
	defer func() {
		if rec := recover(); rec != nil {
			s.fail(fmt.Sprintf("panic during storage migration: %v", rec))
		}
		s.redis.Del(context.Background(), storageMigrationLockKey)
		s.redis.Del(context.Background(), storageMigrationCancelKey)
	}()

	stopHeartbeat := s.startHeartbeat()
	defer stopHeartbeat()

	isCancelled := func() bool { return s.cancelled(jobID) }
	logLine := func(line string) { s.update(func(j *StorageMigrationJob) { j.appendLog(line) }) }

	var (
		manifest *models.StorageManifest
		entries  []models.StorageManifestEntry
		err      error
	)

	// --- manifesting ---
	if req.Kind == StorageJobMigrate || req.Kind == StorageJobManifest {
		s.update(func(j *StorageMigrationJob) {
			j.Phase = StoragePhaseManifesting
			j.appendLog("Inventorying the source: every object is read once and checksummed.")
		})
		manifest, entries, err = storagemigrate.CaptureManifest(ctx, src, s.store, storagemigrate.CaptureOptions{
			BackendLabel: s.sourceLabel(),
			CreatedBy:    s.startedBy(),
			Log:          logLine,
			Cancelled:    isCancelled,
			Progress: func(done, total, bytesDone, bytesTotal int64, key string) {
				s.update(func(j *StorageMigrationJob) {
					j.ObjectsDone, j.ObjectsTotal = done, total
					j.BytesDone, j.BytesTotal = bytesDone, bytesTotal
					j.CurrentKey = key
				})
			},
		})
		if errors.Is(err, storagemigrate.ErrCaptureCancelled) {
			s.cancel("Cancelled during manifesting. The source was never modified.")
			return
		}
		if err != nil {
			s.fail("capture manifest: " + err.Error())
			return
		}
		s.update(func(j *StorageMigrationJob) {
			j.ManifestID = manifest.ID
			j.ObjectsTotal = manifest.ObjectCount
			j.ObjectsDone = manifest.ObjectCount
			j.BytesTotal = manifest.TotalBytes
			j.BytesDone = manifest.TotalBytes
		})
	}

	// --- verify jobs load the stored manifest instead ---
	if req.Kind == StorageJobVerify {
		manifest, err = s.store.GetStorageManifest(req.ManifestID)
		if err != nil {
			s.fail("load manifest: " + err.Error())
			return
		}
		if manifest == nil {
			s.fail(fmt.Sprintf("manifest #%d does not exist", req.ManifestID))
			return
		}
		if manifest.DataSet != req.DataSet {
			s.fail(fmt.Sprintf("manifest #%d was captured for data set %q, not %q", req.ManifestID, manifest.DataSet, req.DataSet))
			return
		}
		entries, err = s.store.ListStorageManifestEntries(manifest.ID)
		if err != nil {
			s.fail("load manifest entries: " + err.Error())
			return
		}
		s.update(func(j *StorageMigrationJob) {
			j.ManifestID = manifest.ID
			j.ObjectsTotal = manifest.ObjectCount
			j.BytesTotal = manifest.TotalBytes
		})
	}

	if req.Kind == StorageJobManifest {
		s.finish("Manifest captured. Move the data out of band, reconfigure the backend, then run a verification against this manifest.")
		return
	}

	// --- copying ---
	if req.Kind == StorageJobMigrate {
		s.update(func(j *StorageMigrationJob) {
			j.Phase = StoragePhaseCopying
			j.ObjectsDone, j.BytesDone, j.ObjectsSkipped = 0, 0, 0
			j.appendLog("Copying. Objects already identical by checksum are skipped.")
		})
		_, cerr := storagemigrate.CopyAll(ctx, src, target, entries, storagemigrate.CopyOptions{
			Log:       logLine,
			Cancelled: isCancelled,
			Progress: func(done, skipped, total, bytesDone, bytesTotal int64, key string) {
				s.update(func(j *StorageMigrationJob) {
					j.ObjectsDone, j.ObjectsSkipped, j.ObjectsTotal = done, skipped, total
					j.BytesDone, j.BytesTotal = bytesDone, bytesTotal
					j.CurrentKey = key
				})
			},
		})
		if errors.Is(cerr, storagemigrate.ErrCopyCancelled) {
			s.cancel("Cancelled during copying. The source was never modified; re-running with the same manifest resumes.")
			return
		}
		if cerr != nil {
			s.fail("copy: " + cerr.Error())
			return
		}
	}

	// --- verifying ---
	s.update(func(j *StorageMigrationJob) {
		j.Phase = StoragePhaseVerifying
		j.ObjectsDone, j.BytesDone = 0, 0
		j.appendLog(fmt.Sprintf("Verifying (%s). Presence is always checked in full; %s.",
			req.VerifyMode, verifyScopeNote(req.VerifyMode)))
	})
	report, verr := storagemigrate.Verify(ctx, target, manifest, entries, req.VerifyMode, storagemigrate.VerifyOptions{
		Log:       logLine,
		Cancelled: isCancelled,
		Progress: func(done, _, total, bytesDone, bytesTotal int64, key string) {
			s.update(func(j *StorageMigrationJob) {
				j.ObjectsDone, j.ObjectsTotal = done, total
				j.BytesDone, j.BytesTotal = bytesDone, bytesTotal
				j.CurrentKey = key
			})
		},
	})
	if errors.Is(verr, storagemigrate.ErrVerifyCancelled) {
		s.cancel("Cancelled during verification. Nothing was modified on either side.")
		return
	}
	if verr != nil {
		s.fail("verify: " + verr.Error())
		return
	}
	s.update(func(j *StorageMigrationJob) { j.Verify = &report })

	// Partial failure is failure. There is no "completed with warnings".
	if !report.OK {
		s.fail(fmt.Sprintf("verification found %d problem(s); the source was NOT modified", report.ProblemsTotal))
		return
	}

	// --- switching_config ---
	//
	// configSettled answers "is it safe to delete the source now?". It is true
	// if and ONLY if SwitchConfig actually succeeded and the active config now
	// points at the target. It is what gets handed to AuthorizeSourceDelete, so
	// the delete cannot run ahead of the switch.
	//
	// It is deliberately NOT set for a data-set-to-data-set migrate. "The
	// request named a targetDataSet" is not the same claim as "no Core-managed
	// config points at this source", and nothing here can check the second one;
	// Validate refuses deleteSource for that shape instead.
	configSettled := false
	if req.Kind == StorageJobMigrate {
		if serr := storagemigrate.AuthorizeConfigSwitch(&report); serr != nil {
			s.fail(serr.Error())
			return
		}
		if req.TargetConfig != nil {
			s.update(func(j *StorageMigrationJob) {
				j.Phase = StoragePhaseSwitchingConfig
				j.CurrentKey = ""
				j.appendLog("Verification passed. Pointing the active storage config at the target. This phase is not cancellable.")
			})
			if serr := s.resolver.SwitchConfig(ctx, req.DataSet, *req.TargetConfig); serr != nil {
				// The copy succeeded and verified, so the data is now in BOTH
				// places and the OLD config is still live. Say exactly that,
				// and delete nothing: an operator told only "switch failed"
				// might start cleaning up the wrong side.
				s.fail(storagemigrate.SwitchFailureReport(s.sourceLabel(), s.targetLabel()) + " (" + serr.Error() + ")")
				return
			}
			configSettled = true
			s.update(func(j *StorageMigrationJob) {
				j.ConfigSwitched = true
				j.appendLog("Active config switched to the target. The system now reads from the target.")
			})
		} else {
			// A data-set-to-data-set pairing: the engine has no config to
			// repoint. ConfigSwitched and configSettled both stay false because
			// nothing was switched; say so rather than implying the engine
			// repointed something.
			s.update(func(j *StorageMigrationJob) {
				// Says what the engine DID, not what configs exist. Whether
				// something still points at the source is unknowable from
				// here, and an operator reads this line while deciding what to
				// clean up - the same over-claiming that had to be removed
				// from the delete log.
				j.appendLog("This is a data-set-to-data-set migrate, so the engine did not switch any config. Repoint the consuming subsystem at the target yourself, and remove the old copy only once you have.")
			})
		}
	}

	// --- deleting_source ---
	if req.Kind == StorageJobMigrate && req.DeleteSource {
		if aerr := storagemigrate.AuthorizeSourceDelete(req.DeleteSource, req.VerifyMode, &report, configSettled); aerr != nil {
			s.fail(aerr.Error())
			return
		}
		s.update(func(j *StorageMigrationJob) {
			j.Phase = StoragePhaseDeleting
			j.ObjectsDone, j.BytesDone = 0, 0
			j.appendLog("Verification passed, the config is settled and the delete was opted in. Removing the source objects. This phase is not cancellable.")
		})
		if _, derr := storagemigrate.DeleteSource(ctx, src, entries, storagemigrate.DeleteOptions{
			Log: logLine,
			Progress: func(done, _, total, bytesDone, bytesTotal int64, key string) {
				s.update(func(j *StorageMigrationJob) {
					j.ObjectsDone, j.ObjectsTotal = done, total
					j.BytesDone, j.BytesTotal = bytesDone, bytesTotal
					j.CurrentKey = key
				})
			},
		}); derr != nil {
			s.fail("delete source: " + derr.Error())
			return
		}
	}

	s.finish(finishMessage(req, report))
}

func verifyScopeNote(mode string) string {
	if mode == storagemigrate.VerifyModeSample {
		return "contents are hashed for a bounded sample only"
	}
	return "contents are hashed for every object"
}

func finishMessage(req StorageMigrationRequest, report storagemigrate.StorageVerifyReport) string {
	switch {
	case req.Kind == StorageJobVerify && report.Mode == storagemigrate.VerifyModeSample:
		return fmt.Sprintf("SAMPLE PASS: nothing wrong in the %.1f%% of objects checked. This is not a full guarantee.", report.CheckedFraction*100)
	case req.Kind == StorageJobVerify:
		return "Verification passed against every object in the manifest."
	// Validate refuses deleteSource unless the target is a targetConfig, so
	// there is no "source removed but nothing repointed" outcome to describe:
	// that is exactly the ordering the delete gate exists to prevent, and an
	// arm here claiming it happened would advertise a path that cannot run.
	case req.DeleteSource:
		return "Migration complete: verified in full, the active config now points at the target, and the old copy has been removed."
	case req.TargetConfig != nil && report.Mode == storagemigrate.VerifyModeSample:
		return fmt.Sprintf("Copy complete and the active config now points at the target. SAMPLE PASS over %.1f%% of objects; the old copy was left in place. Remove it yourself once you are satisfied - a sampled verification can never authorize that automatically.", report.CheckedFraction*100)
	case req.TargetConfig != nil:
		return "Copy complete, verified in full, and the active config now points at the target. The old copy was left in place; remove it when you are ready."
	case report.Mode == storagemigrate.VerifyModeSample:
		return fmt.Sprintf("Copy complete. SAMPLE PASS over %.1f%% of objects; the source was left in place.", report.CheckedFraction*100)
	default:
		return "Copy complete and verified in full. The source was left in place; remove it once you have switched the backend over."
	}
}

func (s *StorageMigrationService) finish(msg string) {
	finished := time.Now()
	s.update(func(j *StorageMigrationJob) {
		j.Phase = StoragePhaseDone
		j.FinishedAt = &finished
		j.CurrentKey = ""
		j.appendLog("DONE. " + msg)
	})
	log.Printf("storage-migration: job %s finished", s.jobID())
}

func (s *StorageMigrationService) fail(msg string) {
	log.Printf("storage-migration: FAILED: %s", msg)
	finished := time.Now()
	s.update(func(j *StorageMigrationJob) {
		j.Phase = StoragePhaseFailed
		j.Error = msg
		j.FinishedAt = &finished
		j.CurrentKey = ""
		j.appendLog("FAILED: " + msg)
		j.appendLog("The source was NOT modified. Re-running with the same manifest resumes where this left off.")
	})
}

func (s *StorageMigrationService) cancel(msg string) {
	finished := time.Now()
	s.update(func(j *StorageMigrationJob) {
		j.Phase = StoragePhaseCancelled
		j.FinishedAt = &finished
		j.CurrentKey = ""
		j.appendLog("CANCELLED: " + msg)
	})
}

// startHeartbeat keeps the lock alive and refreshes UpdatedAt so a long
// single-object copy is not mistaken for a dead job. Returns a stop function.
func (s *StorageMigrationService) startHeartbeat() func() {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(storageHeartbeat)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				s.redis.Expire(context.Background(), storageMigrationLockKey, storageMigrationLockTTL)
				s.update(func(j *StorageMigrationJob) {}) // touch UpdatedAt + re-persist
			}
		}
	}()
	return func() { close(done) }
}

// update mutates the in-memory job under lock, bumps UpdatedAt, and persists.
func (s *StorageMigrationService) update(mut func(*StorageMigrationJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil {
		return
	}
	mut(s.job)
	s.job.UpdatedAt = time.Now()
	s.persistLocked(s.job)
}

// persistLocked writes the job JSON to Redis. Caller holds s.mu.
//
// Everything in this struct is non-secret by construction: the labels come
// from the resolver, which produces endpoint+bucket+prefix at most, and the
// log lines are written by this package and by storagemigrate, neither of
// which ever sees a credential.
func (s *StorageMigrationService) persistLocked(job *StorageMigrationJob) {
	b, err := json.Marshal(job)
	if err != nil {
		return
	}
	s.redis.Set(context.Background(), storageMigrationJobKey, b, storageJobTTL)
}

func (s *StorageMigrationService) jobID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil {
		return ""
	}
	return s.job.ID
}

func (s *StorageMigrationService) sourceLabel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil {
		return ""
	}
	return s.job.SourceLabel
}

func (s *StorageMigrationService) targetLabel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil {
		return ""
	}
	return s.job.TargetLabel
}

func (s *StorageMigrationService) startedBy() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil {
		return ""
	}
	return s.job.StartedBy
}
