package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"dylaris-core/models"
	backupstorage "dylaris-core/storage/backup"
	"dylaris-core/store"

	"github.com/redis/go-redis/v9"
)

// BackupRunStarter is implemented by anything that can dispatch a backup
// command to a node. The handlers package owns the canonical dispatch logic
// — the scheduler simply asks the same Store for due jobs and pushes
// commands directly to Redis (mirrors handlers.startBackupRun without the
// HTTP-specific bits to avoid an import cycle).
type BackupScheduler struct {
	store store.Store
	redis *redis.Client
}

func NewBackupScheduler(s store.Store, r *redis.Client) *BackupScheduler {
	return &BackupScheduler{store: s, redis: r}
}

// Start runs the scheduler tick loop until ctx is cancelled. Polls every
// 60s — backup work isn't latency-sensitive and frequent polling would just
// hammer the DB. Also subscribes to backup result messages from nodes.
func (b *BackupScheduler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		b.tick(ctx) // immediate first run
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
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
	var storage *models.BackupStorage
	if job.StorageID != nil {
		storage, _ = b.store.GetBackupStorage(*job.StorageID)
	}
	if storage == nil {
		return
	}
	// Best-effort delete from storage — DB rows are already gone.
	for _, run := range pruned {
		if err := b.deleteStorageObject(ctx, storage, run.StorageKey); err != nil {
			log.Printf("retention prune: %s delete failed: %v", run.StorageKey, err)
		}
	}
}

// deleteStorageObject opens the configured backend and deletes a single
// object. Used by the retention pass after a successful run.
func (b *BackupScheduler) deleteStorageObject(ctx context.Context, bs *models.BackupStorage, key string) error {
	provider, err := backupstorage.Open(ctx, bs)
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

	storageKey := fmt.Sprintf("backups/%s/job-%d/%s.tar.gz", srv.UUID, job.ID, time.Now().UTC().Format("20060102-150405"))
	runID, err := b.store.CreateBackupRun(&models.BackupRun{
		JobID:      job.ID,
		Status:     "running",
		StorageKey: storageKey,
	})
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	storageCfgJSON, _ := json.Marshal(storage)
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
	}
	jsonData, _ := json.Marshal(payload)
	queueKey := fmt.Sprintf("dylaris:node:%s:queue", node.Token)
	if err := b.redis.RPush(ctx, queueKey, jsonData).Err(); err != nil {
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
