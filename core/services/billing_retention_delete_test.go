package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"dylaris-core/models"
	"dylaris-core/store"
)

// retentionFakeStore is billingFakeStore's sibling for the part that actually
// deletes data. It records which run rows were dropped so a test can prove a row
// SURVIVED - the interesting assertion here is the negative one.
type retentionFakeStore struct {
	store.Store

	runs        []store.BackupRunRef
	byID        map[int]*models.BackupStorage
	def         *models.BackupStorage
	defErr      error
	deletedRuns []int
}

func (f *retentionFakeStore) ListBackupRunsByOwner(string) ([]store.BackupRunRef, error) {
	return f.runs, nil
}

func (f *retentionFakeStore) GetBackupStorage(id int) (*models.BackupStorage, error) {
	return f.byID[id], nil
}

func (f *retentionFakeStore) GetDefaultBackupStorage() (*models.BackupStorage, error) {
	return f.def, f.defErr
}

func (f *retentionFakeStore) DeleteBackupRun(id int) error {
	f.deletedRuns = append(f.deletedRuns, id)
	return nil
}

// localStorageAt lives in backup_reaper_test.go - a real "local" backend rooted
// at a temp dir, which is what these tests need too: whether the archive is gone
// is a property of the provider, not of anything stubbed here.

func writeArchive(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("archive"), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return p
}

// The regression. A backup job created with the panel's FIRST storage option,
// "Default storage", stores storage_id NULL. Retention used to read that as "no
// storage", skip the object deletion entirely, and delete the run row anyway.
//
// The archive then outlives the retention window with nothing pointing at it:
// the tenant's data is still on the platform after the point it was promised to
// be gone, the operator keeps paying for the bytes, and the row that would let
// either be found has been deleted. Same NULL-means-default confusion that made
// these backups unrestorable (1c08ded); this site was missed then.
func TestDeleteTenantBackupsResolvesTheDefaultStorage(t *testing.T) {
	dir := t.TempDir()
	archive := writeArchive(t, dir, "run-1.tar.gz")

	fs := &retentionFakeStore{
		runs: []store.BackupRunRef{{RunID: 1, StorageKey: "run-1.tar.gz", StorageID: nil}},
		def:  localStorageAt(t, dir),
	}
	svc := &BillingLifecycleService{store: fs}

	svc.deleteTenantBackups(context.Background(), "u1")

	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Error("the archive is still on disk after retention expired; a NULL storage_id means the platform default, not 'no storage'")
	}
	if len(fs.deletedRuns) != 1 || fs.deletedRuns[0] != 1 {
		t.Errorf("deletedRuns = %v, want [1]", fs.deletedRuns)
	}
}

// An explicit storage id keeps working; this is the case that always did.
func TestDeleteTenantBackupsUsesAnExplicitStorage(t *testing.T) {
	dir := t.TempDir()
	archive := writeArchive(t, dir, "run-2.tar.gz")

	id := 7
	fs := &retentionFakeStore{
		runs: []store.BackupRunRef{{RunID: 2, StorageKey: "run-2.tar.gz", StorageID: &id}},
		byID: map[int]*models.BackupStorage{7: localStorageAt(t, dir)},
		// A default that would delete nothing, so a test that silently fell back
		// to it instead of honouring the id would fail rather than pass.
		def: localStorageAt(t, t.TempDir()),
	}
	svc := &BillingLifecycleService{store: fs}

	svc.deleteTenantBackups(context.Background(), "u1")

	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Error("the archive named by an explicit storage id was not deleted")
	}
	if len(fs.deletedRuns) != 1 {
		t.Errorf("deletedRuns = %v, want one row deleted", fs.deletedRuns)
	}
}

// When the object cannot be deleted the row must SURVIVE. Dropping it turns a
// transient fault into the same permanent orphan the bug above produced, and
// nothing would ever retry because the pointer is gone. The hourly pass is
// idempotent, so keeping the row costs one retry.
func TestDeleteTenantBackupsKeepsTheRowWhenTheObjectSurvives(t *testing.T) {
	fs := &retentionFakeStore{
		runs:   []store.BackupRunRef{{RunID: 3, StorageKey: "run-3.tar.gz", StorageID: nil}},
		defErr: errors.New("database unreachable"),
	}
	svc := &BillingLifecycleService{store: fs}

	svc.deleteTenantBackups(context.Background(), "u1")

	if len(fs.deletedRuns) != 0 {
		t.Errorf("deletedRuns = %v, want none — the object was never confirmed gone, so the row is the only thing left pointing at it", fs.deletedRuns)
	}
}

// An unknown provider cannot be opened. Same rule: keep the row.
func TestDeleteTenantBackupsKeepsTheRowWhenTheStorageCannotBeOpened(t *testing.T) {
	fs := &retentionFakeStore{
		runs: []store.BackupRunRef{{RunID: 4, StorageKey: "run-4.tar.gz", StorageID: nil}},
		def:  &models.BackupStorage{ID: 1, Name: "broken", Provider: "not-a-provider"},
	}
	svc := &BillingLifecycleService{store: fs}

	svc.deleteTenantBackups(context.Background(), "u1")

	if len(fs.deletedRuns) != 0 {
		t.Errorf("deletedRuns = %v, want none", fs.deletedRuns)
	}
}

// An object that is already gone must NOT keep the row alive forever. Both
// non-node providers report a missing object as success (S3 maps NoSuchKey to
// nil, local ignores os.IsNotExist), so the row goes on the first pass and the
// retry loop above cannot spin on a file nobody will ever recreate.
func TestDeleteTenantBackupsDropsTheRowForAnAlreadyMissingObject(t *testing.T) {
	fs := &retentionFakeStore{
		runs: []store.BackupRunRef{{RunID: 5, StorageKey: "never-existed.tar.gz", StorageID: nil}},
		def:  localStorageAt(t, t.TempDir()),
	}
	svc := &BillingLifecycleService{store: fs}

	svc.deleteTenantBackups(context.Background(), "u1")

	if len(fs.deletedRuns) != 1 {
		t.Errorf("deletedRuns = %v, want [5] — a missing object is a completed deletion", fs.deletedRuns)
	}
}
