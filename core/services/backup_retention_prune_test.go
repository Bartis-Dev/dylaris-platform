package services

import (
	"context"
	"os"
	"testing"

	"dylaris-core/models"
	"dylaris-core/store"
)

// pruneFakeStore is the smallest store enforceRetention touches. PruneOldBackupRuns
// deletes the DB rows and reports what it removed, which is exactly why the
// storage lookup after it has to succeed: by then the rows are already gone.
type pruneFakeStore struct {
	store.Store

	job    *models.BackupJob
	pruned []models.BackupRun
	def    *models.BackupStorage
	byID   map[int]*models.BackupStorage
}

func (f *pruneFakeStore) GetBackupJob(int) (*models.BackupJob, error) { return f.job, nil }

func (f *pruneFakeStore) PruneOldBackupRuns(int, int) ([]models.BackupRun, error) {
	return f.pruned, nil
}

func (f *pruneFakeStore) GetBackupStorage(id int) (*models.BackupStorage, error) {
	return f.byID[id], nil
}

func (f *pruneFakeStore) GetDefaultBackupStorage() (*models.BackupStorage, error) {
	return f.def, nil
}

// The worst of the three nil-StorageID sites, because it has no ceiling: it runs
// after EVERY successful backup of a job with a retention count.
//
// PruneOldBackupRuns deletes the rows first and returns them. The old code then
// did `if job.StorageID != nil { storage, _ = ... }; if storage == nil { return }`
// - so for a job on the platform default (storage_id NULL, which is the panel's
// first dropdown option) it returned right there, with the rows already gone and
// the archives untouched. Every retention cycle of every such job left another
// archive behind that nothing knows about, silently and forever.
func TestEnforceRetentionDeletesArchivesOnTheDefaultStorage(t *testing.T) {
	dir := t.TempDir()
	a1 := writeArchive(t, dir, "old-1.tar.gz")
	a2 := writeArchive(t, dir, "old-2.tar.gz")

	fs := &pruneFakeStore{
		job: &models.BackupJob{ID: 7, RetentionCount: 3, StorageID: nil},
		pruned: []models.BackupRun{
			{ID: 1, StorageKey: "old-1.tar.gz"},
			{ID: 2, StorageKey: "old-2.tar.gz"},
		},
		def: localStorageAt(t, dir),
	}
	b := &BackupScheduler{store: fs}

	b.enforceRetention(context.Background(), 7)

	for _, p := range []string{a1, a2} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived the retention prune; its DB row is already gone, so nothing will ever find it again", p)
		}
	}
}

// The explicit-id case, which always worked. Kept so a future change that routes
// everything through the default cannot pass unnoticed: the default here points
// at a DIFFERENT directory, so falling back to it would leave the archive.
func TestEnforceRetentionUsesAnExplicitStorage(t *testing.T) {
	dir := t.TempDir()
	archive := writeArchive(t, dir, "old-3.tar.gz")

	id := 4
	fs := &pruneFakeStore{
		job:    &models.BackupJob{ID: 8, RetentionCount: 3, StorageID: &id},
		pruned: []models.BackupRun{{ID: 3, StorageKey: "old-3.tar.gz"}},
		byID:   map[int]*models.BackupStorage{4: localStorageAt(t, dir)},
		def:    localStorageAt(t, t.TempDir()),
	}
	b := &BackupScheduler{store: fs}

	b.enforceRetention(context.Background(), 8)

	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Error("the archive named by an explicit storage id survived the prune")
	}
}

// With no storage configured at all there is nothing to delete from and nothing
// to do; it must not panic on the way out.
func TestEnforceRetentionSurvivesNoStorageAtAll(t *testing.T) {
	fs := &pruneFakeStore{
		job:    &models.BackupJob{ID: 9, RetentionCount: 3, StorageID: nil},
		pruned: []models.BackupRun{{ID: 4, StorageKey: "orphan.tar.gz"}},
	}
	b := &BackupScheduler{store: fs}
	b.enforceRetention(context.Background(), 9)
}
