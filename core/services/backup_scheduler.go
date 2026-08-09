package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"time"

	nodegrpc "dylaris-core/grpc"
	"dylaris-core/models"
	"dylaris-core/pkg/leader"
	backupstorage "dylaris-core/storage/backup"
	"dylaris-core/store"

	"github.com/redis/go-redis/v9"
)

// BackupScheduler is leader-gated: it ticks every minute, lists due backup
// jobs, dispatches them to nodes via Redis, and advances next_run_at. It
// pushes commands directly to Redis (mirroring handlers.startBackupRun
// without the HTTP-specific bits) to avoid an import cycle with handlers.
type BackupScheduler struct {
	store    store.Store
	redis    *redis.Client
	queue    *QueueService       // publishes backup_run to the node's :cmds stream (BC1)
	registry *nodegrpc.Registry // optional — required only for node-local retention deletes
	leader   leader.Election
	// coreStorage opens the shared Core file storage as a backup backend.
	// Optional; required only for jobs whose storage row is "core-storage".
	coreStorage func(subPrefix string) (backupstorage.Storage, error)
}

func NewBackupScheduler(s store.Store, r *redis.Client, q *QueueService) *BackupScheduler {
	return &BackupScheduler{store: s, redis: r, queue: q}
}

// SetRegistry wires the gRPC mesh registry so the retention sweep can call
// into Nodes to delete node-local backups. Optional: a scheduler running
// without a registry simply logs and skips node-local deletes (the Node-side
// retention pass still trims its own folder).
func (b *BackupScheduler) SetRegistry(reg *nodegrpc.Registry) {
	b.registry = reg
}

// SetCoreStorage wires the builder that opens the shared Core file storage as
// a backup backend. Without it, backupstorage.Open refuses every job whose
// storage row is "core-storage", so the reaper's probe could never reach one:
// it reported "could not be determined" for the whole provider rather than
// present-or-absent, which is the answer the reaper exists to give.
//
// Supplied from main.go, which owns the AppState the builder needs. Optional,
// like SetRegistry: a scheduler without it still reaps, it just cannot inspect
// core-storage-backed archives.
func (b *BackupScheduler) SetCoreStorage(fn func(subPrefix string) (backupstorage.Storage, error)) {
	b.coreStorage = fn
}

// SetLeader wires the leader-election gate. Without it the scheduler ticks
// + processes Pub/Sub messages on every Core (single-instance dev mode).
// With it, only the elected Core does the work — followers still subscribe
// to Pub/Sub but ignore the messages, which keeps the message shape on the
// node side unchanged. Conversion to XReadGroup for true competing-consumer
// semantics is a follow-up.
func (b *BackupScheduler) SetLeader(l leader.Election) { b.leader = l }

// Start runs the scheduler tick loop until ctx is cancelled. Polls every
// 60s — backup work isn't latency-sensitive and frequent polling would just
// hammer the DB. Also subscribes to backup result messages from nodes.
func (b *BackupScheduler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		if b.leader == nil || b.leader.IsLeader() {
			b.tick(ctx) // immediate first run only on the leader
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if b.leader != nil && !b.leader.IsLeader() {
					continue
				}
				b.tick(ctx)
			}
		}
	}()
	go b.consumeResults(ctx)
	go b.consumeRestoreResults(ctx)
}

// consumeRestoreResults listens on dylaris:backup:restores and updates the
// backup_restores row with the outcome reported by the node.
func (b *BackupScheduler) consumeRestoreResults(ctx context.Context) {
	pubsub := b.redis.Subscribe(ctx, "dylaris:backup:restores")
	defer pubsub.Close()
	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// Leader-gate: Pub/Sub broadcasts to every
			// subscriber, so without this guard each Core writes the
			// same backup_restores row N times. Only the leader updates.
			if b.leader != nil && !b.leader.IsLeader() {
				continue
			}
			var result struct {
				RestoreID int    `json:"restoreId"`
				Status    string `json:"status"`
				Error     string `json:"error"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &result); err != nil {
				log.Printf("restore result: decode failed: %v", err)
				continue
			}
			if result.RestoreID == 0 {
				continue
			}
			completed := time.Time{}
			if result.Status == "success" || result.Status == "failed" {
				completed = time.Now()
			}
			if err := b.store.UpdateBackupRestoreStatus(result.RestoreID, result.Status, result.Error, completed); err != nil {
				log.Printf("restore result: update failed for id=%d: %v", result.RestoreID, err)
			}
		}
	}
}

// consumeResults listens on the `dylaris:backup:results` channel and
// updates the backup_runs row when a node finishes (success or failure).
// Also prunes old runs from storage when retention limits are exceeded.
func (b *BackupScheduler) consumeResults(ctx context.Context) {
	pubsub := b.redis.Subscribe(ctx, "dylaris:backup:results")
	defer pubsub.Close()
	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// Leader-gate: same Pub/Sub broadcast issue as in
			// consumeRestoreResults — only the leader processes results.
			if b.leader != nil && !b.leader.IsLeader() {
				continue
			}
			var result struct {
				RunID     int    `json:"runId"`
				Status    string `json:"status"`
				Error     string `json:"error"`
				SizeBytes int64  `json:"sizeBytes"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &result); err != nil {
				log.Printf("backup result: decode failed: %v", err)
				continue
			}
			run, err := b.store.GetBackupRun(result.RunID)
			if err != nil {
				log.Printf("backup result: run %d not found", result.RunID)
				continue
			}
			b.store.UpdateBackupRunStatus(result.RunID, result.Status, result.Error, result.SizeBytes, run.StorageKey, time.Now())
			if result.Status == "success" {
				b.enforceRetention(ctx, run.JobID)
			}
		}
	}
}

// enforceRetention deletes successful runs that exceed the job's retention
// count, both from the DB and from the underlying storage provider.
func (b *BackupScheduler) enforceRetention(ctx context.Context, jobID int) {
	job, err := b.store.GetBackupJob(jobID)
	if err != nil {
		return
	}
	pruned, err := b.store.PruneOldBackupRuns(jobID, job.RetentionCount)
	if err != nil || len(pruned) == 0 {
		return
	}
	// A nil StorageID means the platform default, not "no storage". Reading it as
	// the latter left `storage` nil and returned HERE - after PruneOldBackupRuns
	// above had already deleted the rows. So on a job using the panel's default
	// storage option, every retention cycle dropped the records and left the
	// archives behind, permanently and without a log line. That is the failure
	// mode with no ceiling: it repeats on every prune, for every such job, and
	// nothing afterwards knows those objects exist.
	storage, err := ResolveJobStorage(b.store, job.StorageID)
	if err != nil {
		log.Printf("retention prune: job %d — cannot resolve storage: %v; %d pruned archive(s) are now untracked",
			jobID, err, len(pruned))
		return
	}
	// Best-effort delete from storage — DB rows are already gone. That ordering
	// is pre-existing: PruneOldBackupRuns both prunes and reports, so a failure
	// here still orphans that one archive. Resolving the storage correctly turns
	// "always orphans" into "orphans on a transient fault", which is the part
	// worth fixing without restructuring the store call.
	for _, run := range pruned {
		if err := b.deleteStorageObject(ctx, storage, run.StorageKey); err != nil {
			log.Printf("retention prune: %s delete failed: %v", run.StorageKey, err)
		}
	}
}

// storageDeps is the single place the scheduler's backup-storage dependencies
// are assembled, so a provider that needs one of them cannot work on one code
// path and be refused on another. That is what happened to core-storage: the
// retention delete and the reaper each built their own Deps literal without a
// CoreStorage builder.
func (b *BackupScheduler) storageDeps() backupstorage.Deps {
	return backupstorage.Deps{
		Registry:    b.registry,
		NodeStore:   b.store,
		CoreStorage: b.coreStorage,
	}
}

// deleteStorageObject opens the configured backend and deletes a single
// object. Used by the retention pass after a successful run.
func (b *BackupScheduler) deleteStorageObject(ctx context.Context, bs *models.BackupStorage, key string) error {
	deps := b.storageDeps()
	provider, err := backupstorage.Open(ctx, bs, deps)
	if err != nil {
		return err
	}
	return provider.Delete(ctx, key)
}

func (b *BackupScheduler) tick(ctx context.Context) {
	if b.store == nil || b.redis == nil {
		return
	}
	now := time.Now()
	jobs, err := b.store.ListDueBackupJobs(now)
	if err != nil {
		log.Printf("backup-scheduler: ListDueBackupJobs error: %v", err)
		return
	}
	for _, job := range jobs {
		if err := b.dispatch(ctx, job); err != nil {
			log.Printf("backup-scheduler: job %d dispatch failed: %v", job.ID, err)
		}
	}

	// Runs whose result never came back. Deliberately AFTER dispatch: reaping
	// is cleanup and must never delay this tick's real work.
	b.reapAbandonedRuns(ctx, now)
}

// How long a run may sit at "running" before it is treated as abandoned.
//
// Generous on purpose. The window has to clear the slowest legitimate backup -
// archiving tens of GB on a busy node and uploading it over a home BYON uplink -
// because the cost of being wrong is a scary "failed" against a backup that
// was actually still working.
//
// Being wrong is also recoverable, which is what allows a single fixed number
// here instead of something adaptive: consumeResults updates by run id without
// checking the current status, so a result that finally arrives after the
// reaper gave up overwrites the verdict with the truth, retention included.
const backupRunAbandonedAfter = 6 * time.Hour

// How many abandoned runs one tick will close. Each one may open a storage
// connection, and the first sweep after this ships can meet a backlog that has
// been accumulating since the deployment was installed.
const backupReapBatchSize = 25

// reapAbandonedRuns closes runs whose result never arrived.
//
// It resolves them to "failed" and never to "success", even when the archive
// turns out to be sitting in storage. Core has no confirmation from the node -
// no reported size, no completion signal - so calling such a run successful
// would present an unverified archive as a backup somebody can rely on. That is
// the one error in this whole path that actually hurts, because it is only
// discovered during a restore. The message says what was found instead, and an
// operator can act on that.
//
// Nothing is deleted here for the same reason: an archive that may be a
// complete backup is not something a cleanup routine should remove on a guess.
func (b *BackupScheduler) reapAbandonedRuns(ctx context.Context, now time.Time) {
	runs, err := b.store.ListAbandonedBackupRuns(now.Add(-backupRunAbandonedAfter), backupReapBatchSize)
	if err != nil {
		log.Printf("backup-scheduler: listing abandoned runs failed: %v", err)
		return
	}
	for _, run := range runs {
		size, detail := b.describeAbandonedRun(ctx, run)
		age := now.Sub(run.StartedAt).Round(time.Minute)
		message := fmt.Sprintf("No result was received from the node within %s. %s", age, detail)
		if err := b.store.UpdateBackupRunStatus(run.ID, "failed", message, size, run.StorageKey, now); err != nil {
			log.Printf("backup-scheduler: could not close abandoned run %d: %v", run.ID, err)
			continue
		}
		log.Printf("backup-scheduler: closed abandoned run %d (job %d, started %s ago): %s", run.ID, run.JobID, age, detail)
	}
}

// describeAbandonedRun reports whether an archive turned up where the run was
// going to write one, and returns its size alongside a sentence for the run's
// error message.
//
// The size is recorded even though the run is failed, and it now counts toward
// the storage quota. The object is real bytes on the backend, so the quota
// queries (store/billing.go BackupBytesByOwner, store/traffic.go
// TenantBackupBytes) count any run with a nonzero size, not just successes. A
// node-failed run is always size 0, so this only ever picks up the archives the
// reaper actually found. It is also the most useful single number for deciding
// whether the archive is worth investigating.
func (b *BackupScheduler) describeAbandonedRun(ctx context.Context, run models.BackupRun) (int64, string) {
	if run.StorageKey == "" {
		return 0, "The run never recorded a storage key, so no archive was written."
	}

	obj, err := b.statBackupObject(ctx, run)
	switch {
	case err == nil:
		return obj.Size, fmt.Sprintf(
			"An archive of %d bytes is present at %s. The node never confirmed it, so it is UNVERIFIED - check it before relying on it. It is left in place and counts against the storage quota; delete this run to reclaim the space.",
			obj.Size, run.StorageKey)
	case errors.Is(err, fs.ErrNotExist):
		return 0, fmt.Sprintf("No archive was written to %s, so the backup did not complete.", run.StorageKey)
	default:
		// Storage itself could not answer. Saying "no archive exists" here
		// would be a claim the code cannot make.
		//
		// The error is LOGGED, not stored. This message is rendered to anyone
		// holding backups.read on the server - a tenant, not an operator -
		// while the backend's endpoint and bucket live behind settings.read.
		// A transport failure from the S3 SDK carries the full request URL, so
		// pasting %v here would hand an internal hostname, bucket name and
		// often an internal IP to exactly the audience the settings boundary
		// keeps them from. The storage key is already in the API response, so
		// naming it costs nothing.
		log.Printf("backup-scheduler: could not determine whether run %d wrote an archive at %s: %v", run.ID, run.StorageKey, err)
		return 0, fmt.Sprintf("Whether an archive exists at %s could not be determined. The Core log has the reason.", run.StorageKey)
	}
}

// statBackupObject opens the job's configured backend and stats the run's key.
func (b *BackupScheduler) statBackupObject(ctx context.Context, run models.BackupRun) (backupstorage.Object, error) {
	job, err := b.store.GetBackupJob(run.JobID)
	if err != nil {
		return backupstorage.Object{}, fmt.Errorf("load job: %w", err)
	}
	// Same resolution runJob uses a few lines below. Refusing on a nil
	// StorageID meant a job on the default storage could not even be checked
	// for whether its archive existed, so every one of its runs was reported
	// as UNVERIFIED.
	bs, err := ResolveJobStorage(b.store, job.StorageID)
	if err != nil {
		return backupstorage.Object{}, fmt.Errorf("resolve storage: %w", err)
	}
	provider, err := backupstorage.Open(ctx, bs, b.storageDeps())
	if err != nil {
		return backupstorage.Object{}, fmt.Errorf("open storage: %w", err)
	}
	return provider.Stat(ctx, run.StorageKey)
}

func (b *BackupScheduler) dispatch(ctx context.Context, job models.BackupJob) error {
	srv, err := b.store.GetServerByID(job.ServerID)
	if err != nil {
		return fmt.Errorf("server lookup: %w", err)
	}
	node, err := b.store.GetNodeByID(srv.NodeID)
	if err != nil {
		return fmt.Errorf("node lookup: %w", err)
	}

	// Resolve storage (job-level or fallback to default).
	var storage *models.BackupStorage
	if job.StorageID != nil {
		storage, err = b.store.GetBackupStorage(*job.StorageID)
	} else {
		storage, err = b.store.GetDefaultBackupStorage()
	}
	if err != nil || storage == nil {
		return fmt.Errorf("no storage available")
	}

	// R2 backup quota: skip the scheduled run once the tenant is at/over quota
	// (0/unset = unlimited). The next_run is still advanced below so we don't
	// re-check on a tight loop; the run resumes when usage drops or the limit rises.
	if exceeded, used, quota := R2QuotaExceeded(b.store, srv.OwnerID); exceeded {
		log.Printf("backup-scheduler: job %d skipped — quota reached (%d/%d GB)", job.ID, used/(1<<30), quota/(1<<30))
		next := computeNextRun(job.Schedule, time.Now())
		if next != nil {
			b.store.SetBackupJobScheduled(job.ID, time.Now(), *next)
		}
		return nil
	}
	// Per-server node-local cap - the same gate startBackupRun takes. Cron is
	// the path that actually fills a disk: nobody is watching it, and it runs
	// again every interval forever.
	if exceeded, used, quota := NodeLocalBackupQuotaExceeded(b.store, b.registry, srv); exceeded {
		log.Printf("backup-scheduler: job %d skipped — per-server backup quota reached (%.1f/%.1f GB)",
			job.ID, float64(used)/(1<<30), float64(quota)/(1<<30))
		next := computeNextRun(job.Schedule, time.Now())
		if next != nil {
			b.store.SetBackupJobScheduled(job.ID, time.Now(), *next)
		}
		return nil
	}

	storageKey := fmt.Sprintf("backups/%s/job-%d/%s.tar.gz", srv.UUID, job.ID, time.Now().UTC().Format("20060102-150405"))
	runID, err := b.store.CreateBackupRun(&models.BackupRun{
		JobID:      job.ID,
		Status:     "running",
		StorageKey: storageKey,
	})
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	if b.queue == nil {
		b.store.UpdateBackupRunStatus(runID, "failed", "queue unavailable", 0, "", time.Now())
		return fmt.Errorf("queue unavailable")
	}

	storageCfgJSON, presignedPut := PrepareNodeStorage(ctx, b.store, storage, node, storageKey, "put")
	subServer := ""
	if job.SubServer != nil {
		subServer = *job.SubServer
	}
	payload := map[string]interface{}{
		"action":          "backup_run",
		"runId":           runID,
		"jobId":           job.ID,
		"serverUuid":      srv.UUID,
		"subServer":       subServer,
		"includePatterns": job.IncludePatterns,
		"excludePatterns": job.ExcludePatterns,
		"storageKey":      storageKey,
		"storage":         json.RawMessage(storageCfgJSON),
		"presignedPutUrl": presignedPut,
	}
	// Publish to the node's durable :cmds stream (BC1) instead of RPush to the
	// retired dylaris:node:<token>:queue list, which nothing reads anymore.
	if err := b.queue.SendRawCommand(ctx, node.Token, payload); err != nil {
		b.store.UpdateBackupRunStatus(runID, "failed", "queue push: "+err.Error(), 0, "", time.Now())
		return err
	}

	// Advance next_run_at so we don't re-dispatch on the next tick.
	next := computeNextRun(job.Schedule, time.Now())
	if next != nil {
		b.store.SetBackupJobScheduled(job.ID, time.Now(), *next)
	} else {
		b.store.SetBackupJobScheduled(job.ID, time.Now(), time.Time{})
	}

	return nil
}

// computeNextRun mirrors the helper in handlers/backup.go — duplicated here
// to keep services free of any handlers dependency.
func computeNextRun(schedule string, from time.Time) *time.Time {
	if schedule == "" || schedule == "manual" {
		return nil
	}
	var n int
	var unit string
	if _, err := fmt.Sscanf(schedule, "every %d%s", &n, &unit); err != nil || n <= 0 {
		return nil
	}
	var d time.Duration
	switch unit {
	case "h":
		d = time.Duration(n) * time.Hour
	case "d":
		d = time.Duration(n) * 24 * time.Hour
	default:
		return nil
	}
	next := from.Add(d)
	return &next
}
