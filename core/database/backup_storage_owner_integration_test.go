package database

import (
	"encoding/json"
	"testing"
	"time"

	"dylaris-core/models"
)

// Against a real Postgres, because everything here is SQL that only Postgres
// enforces: the partial unique indexes, the dropped column-level UNIQUE, and a
// quota query whose whole job is a LEFT JOIN that must not drop rows.
//
// Skipped without DYLARIS_TEST_DB_HOST, like the rest of this file's neighbours.

func s3Storage(name string, owner *string, isDefault bool) *models.BackupStorage {
	return &models.BackupStorage{
		Name: name, Provider: "s3", OwnerID: owner, IsDefault: isDefault,
		Config: json.RawMessage(`{"endpoint":"https://s3.example.test","bucket":"b","accessKeyId":"AK"}`),
	}
}

// Two tenants may both call their storage "Backblaze", and the platform still
// may not have two rows by one name.
//
// The old schema had a column-level UNIQUE(name), which would have let the first
// tenant to pick a common name block it for everybody. Replacing it with a plain
// UNIQUE(owner_id, name) would have been the opposite mistake: Postgres treats
// NULLs as distinct, so the platform rows would have stopped being unique at all.
func TestIntegrationBackupStorageNamesAreUniquePerScope(t *testing.T) {
	_, st := integrationDB(t)
	a := newFixture(t, st)
	b := newFixture(t, st)

	name := uniqueName("shared_name_")

	idA, err := st.CreateBackupStorage(s3Storage(name, &a.user.ID, false))
	if err != nil {
		t.Fatalf("first tenant: %v", err)
	}
	t.Cleanup(func() { st.DeleteBackupStorage(idA) })

	idB, err := st.CreateBackupStorage(s3Storage(name, &b.user.ID, false))
	if err != nil {
		t.Fatalf("second tenant with the same name: %v - one tenant must not reserve a name globally", err)
	}
	t.Cleanup(func() { st.DeleteBackupStorage(idB) })

	// The platform keeps its own uniqueness.
	platformName := uniqueName("platform_")
	idP, err := st.CreateBackupStorage(s3Storage(platformName, nil, false))
	if err != nil {
		t.Fatalf("platform storage: %v", err)
	}
	t.Cleanup(func() { st.DeleteBackupStorage(idP) })
	if _, err := st.CreateBackupStorage(s3Storage(platformName, nil, false)); err == nil {
		t.Error("a duplicate PLATFORM name was accepted; uniqueness stopped being enforced there")
	}
}

// A tenant marking their own storage as their default must not clear the
// platform default, which is what every other tenant falls back to.
func TestIntegrationTenantDefaultLeavesThePlatformDefaultAlone(t *testing.T) {
	_, st := integrationDB(t)
	f := newFixture(t, st)

	platformID, err := st.CreateBackupStorage(s3Storage(uniqueName("plat_def_"), nil, true))
	if err != nil {
		t.Fatalf("platform default: %v", err)
	}
	t.Cleanup(func() { st.DeleteBackupStorage(platformID) })

	mineID, err := st.CreateBackupStorage(s3Storage(uniqueName("mine_def_"), &f.user.ID, true))
	if err != nil {
		t.Fatalf("tenant default: %v", err)
	}
	t.Cleanup(func() { st.DeleteBackupStorage(mineID) })

	def, err := st.GetDefaultBackupStorage()
	if err != nil {
		t.Fatalf("GetDefaultBackupStorage: %v", err)
	}
	if def == nil || def.ID != platformID {
		t.Errorf("platform default = %v, want %d - a tenant's choice cleared it", def, platformID)
	}
	mine, err := st.GetUserDefaultBackupStorage(f.user.ID)
	if err != nil {
		t.Fatalf("GetUserDefaultBackupStorage: %v", err)
	}
	if mine == nil || mine.ID != mineID {
		t.Errorf("tenant default = %v, want %d", mine, mineID)
	}
	// And it is theirs alone.
	other := newFixture(t, st)
	if got, _ := st.GetUserDefaultBackupStorage(other.user.ID); got != nil {
		t.Errorf("another tenant sees %v as their default", got)
	}
}

// The platform list must not show tenants' private buckets: it feeds the admin
// screen and the storage dropdown, where every entry is offerable as a target
// for somebody else's backups.
func TestIntegrationPlatformListExcludesTenantStorages(t *testing.T) {
	_, st := integrationDB(t)
	f := newFixture(t, st)

	mineID, err := st.CreateBackupStorage(s3Storage(uniqueName("hidden_"), &f.user.ID, false))
	if err != nil {
		t.Fatalf("tenant storage: %v", err)
	}
	t.Cleanup(func() { st.DeleteBackupStorage(mineID) })

	list, err := st.ListBackupStorages()
	if err != nil {
		t.Fatalf("ListBackupStorages: %v", err)
	}
	for _, bs := range list {
		if bs.ID == mineID {
			t.Fatalf("the platform list offered a tenant's own bucket: %+v", bs)
		}
	}
	own, err := st.ListBackupStoragesByOwner(f.user.ID)
	if err != nil {
		t.Fatalf("ListBackupStoragesByOwner: %v", err)
	}
	if len(own) != 1 || own[0].ID != mineID {
		t.Errorf("the tenant's own list = %+v, want just %d", own, mineID)
	}
}

// The quota counts what a tenant stores ON OURS and nothing else.
//
// The exclusion keys off the RUN's storage_id, and the query is a LEFT JOIN so
// that a run with no storage recorded still counts. An inner join there would
// have silently exempted every archive taken before the column existed - a
// quota that stops enforcing itself for the oldest data is worse than no quota,
// because it still reads as working.
func TestIntegrationBackupQuotaExcludesTenantOwnedStorage(t *testing.T) {
	_, st := integrationDB(t)
	f := newFixture(t, st)

	oursID, err := st.CreateBackupStorage(s3Storage(uniqueName("ours_"), nil, false))
	if err != nil {
		t.Fatalf("platform storage: %v", err)
	}
	t.Cleanup(func() { st.DeleteBackupStorage(oursID) })
	theirsID, err := st.CreateBackupStorage(s3Storage(uniqueName("theirs_"), &f.user.ID, false))
	if err != nil {
		t.Fatalf("tenant storage: %v", err)
	}
	t.Cleanup(func() { st.DeleteBackupStorage(theirsID) })

	job := &models.BackupJob{
		ServerID: f.server.ID, Name: uniqueName("job_"), Schedule: "manual",
		IncludePatterns: []string{}, ExcludePatterns: []string{}, RetentionCount: 3, Enabled: true,
	}
	jobID, err := st.CreateBackupJob(job)
	if err != nil {
		t.Fatalf("CreateBackupJob: %v", err)
	}
	t.Cleanup(func() { st.DeleteBackupJob(jobID) })

	mkRun := func(storageID *int, size int64) {
		t.Helper()
		runID, err := st.CreateBackupRun(&models.BackupRun{
			JobID: jobID, Status: "running", StorageKey: uniqueName("k_"), StorageID: storageID,
		})
		if err != nil {
			t.Fatalf("CreateBackupRun: %v", err)
		}
		if err := st.UpdateBackupRunStatus(runID, "success", "", size, uniqueName("k_"), time.Now()); err != nil {
			t.Fatalf("UpdateBackupRunStatus: %v", err)
		}
	}
	mkRun(&oursID, 100)
	mkRun(&theirsID, 900)
	mkRun(nil, 7) // predates the column: read as ours, so it counts

	billed, err := st.BackupBytesByOwner(f.user.ID)
	if err != nil {
		t.Fatalf("BackupBytesByOwner: %v", err)
	}
	if billed != 107 {
		t.Errorf("billed bytes = %d, want 107 (ours plus the unrecorded one, never the tenant's own)", billed)
	}
	onOwn, err := st.BackupBytesByOwnerOnOwnStorage(f.user.ID)
	if err != nil {
		t.Fatalf("BackupBytesByOwnerOnOwnStorage: %v", err)
	}
	if onOwn != 900 {
		t.Errorf("own-storage bytes = %d, want 900", onOwn)
	}
}

// Deleting a tenant takes their S3 credentials with them.
//
// SET NULL would be worse than a leak: a row left with no owner becomes a
// PLATFORM storage, visible to admins and selectable as a default.
func TestIntegrationDeletingATenantRemovesTheirStorage(t *testing.T) {
	_, st := integrationDB(t)
	f := newFixture(t, st)

	id, err := st.CreateBackupStorage(s3Storage(uniqueName("goes_away_"), &f.user.ID, false))
	if err != nil {
		t.Fatalf("tenant storage: %v", err)
	}
	// The account cannot be removed while it still holds servers, so the fixture's
	// one goes first. That is the real order too: a tenant is emptied, then deleted.
	if err := st.DeleteServer(f.server.ID); err != nil {
		t.Fatalf("DeleteServer: %v", err)
	}
	if err := st.DeleteUser(f.user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if got, _ := st.GetBackupStorage(id); got != nil {
		t.Errorf("the storage survived its owner as %+v", got)
	}
	// And it certainly did not become a platform row.
	list, _ := st.ListBackupStorages()
	for _, bs := range list {
		if bs.ID == id {
			t.Fatalf("a deleted tenant's storage became a PLATFORM storage: %+v", bs)
		}
	}
}
