package services

import (
	"errors"
	"testing"

	"dylaris-core/models"
)

func ownedBy(id int, owner string) *models.BackupStorage {
	return &models.BackupStorage{ID: id, Name: "tenant bucket", OwnerID: &owner}
}

// The chain, in order: the job's own storage, then the OWNER's default, then the
// platform's.
//
// The middle step is the whole point of the feature. Without it a tenant who
// connected a bucket would have it honoured only on jobs where they picked it by
// hand, and every job left on "Default storage" - which is the panel's first
// option and therefore the common case - would keep writing to ours.
func TestResolveJobStorageWalksTheOwnerChain(t *testing.T) {
	platform := &models.BackupStorage{ID: 1, Name: "platform"}
	mine := ownedBy(5, "u1")
	picked := ownedBy(9, "u1")

	s := fakeBackupStorageStore{
		byID:    map[int]*models.BackupStorage{9: picked},
		def:     platform,
		userDef: map[string]*models.BackupStorage{"u1": mine},
	}

	got, err := ResolveJobStorage(s, intPtr(9), "u1")
	if err != nil || got.ID != 9 {
		t.Errorf("an explicit choice must win: got %v, %v", got, err)
	}
	got, err = ResolveJobStorage(s, nil, "u1")
	if err != nil || got.ID != 5 {
		t.Errorf("no choice should fall to the owner's default: got %v, %v", got, err)
	}
	got, err = ResolveJobStorage(s, nil, "u2")
	if err != nil || got.ID != 1 {
		t.Errorf("a tenant with no default of their own gets the platform's: got %v, %v", got, err)
	}
}

// A job pointing at somebody else's bucket is REFUSED, not quietly redirected.
//
// Both alternatives are wrong in opposite directions: honouring the pointer
// writes one tenant's data into another's storage, and falling through to the
// platform default writes it somewhere the job never named while reporting
// success. An error is the only answer that does neither.
func TestResolveJobStorageRefusesAForeignStorage(t *testing.T) {
	s := fakeBackupStorageStore{
		byID: map[int]*models.BackupStorage{7: ownedBy(7, "someone-else")},
		def:  &models.BackupStorage{ID: 1, Name: "platform"},
	}
	if _, err := ResolveJobStorage(s, intPtr(7), "u1"); !errors.Is(err, ErrForeignBackupStorage) {
		t.Errorf("err = %v, want ErrForeignBackupStorage", err)
	}
}

// An empty owner is "we do not know who is asking", and it must never behave
// like a wildcard. Platform-internal paths pass it, and one of them is the
// retention sweep - which deletes.
func TestResolveJobStorageEmptyOwnerMatchesNoTenant(t *testing.T) {
	s := fakeBackupStorageStore{
		byID:    map[int]*models.BackupStorage{7: ownedBy(7, "u1")},
		def:     &models.BackupStorage{ID: 1},
		userDef: map[string]*models.BackupStorage{"u1": ownedBy(5, "u1")},
	}
	if _, err := ResolveJobStorage(s, intPtr(7), ""); !errors.Is(err, ErrForeignBackupStorage) {
		t.Errorf("an unknown caller reached a tenant's storage: %v", err)
	}
	// And with nothing pointed at, it goes to the platform rather than to some
	// tenant's default.
	got, err := ResolveJobStorage(s, nil, "")
	if err != nil || got.ID != 1 {
		t.Errorf("got %v, %v; want the platform default", got, err)
	}
}

// An archive lives where it was WRITTEN, not where its job points today.
//
// Changing a job's storage used to make every earlier archive unreachable for
// restore, download and deletion while it still showed as present in the panel.
func TestResolveRunStorageUsesTheRunsOwnStorage(t *testing.T) {
	s := fakeBackupStorageStore{
		byID: map[int]*models.BackupStorage{
			3: {ID: 3, Name: "where it was written"},
			8: {ID: 8, Name: "where the job points now"},
		},
		def: &models.BackupStorage{ID: 1},
	}
	run := &models.BackupRun{ID: 42, StorageID: intPtr(3)}
	got, err := ResolveRunStorage(s, run, intPtr(8), "u1")
	if err != nil || got.ID != 3 {
		t.Errorf("got %v, %v; want the storage the run recorded (3)", got, err)
	}
}

// A run with nothing recorded anywhere predates the column, so it went to the
// PLATFORM default - the only place it could have gone, because tenants could
// not have a default yet.
//
// Walking the full chain here would be actively destructive: a tenant who
// connects a bucket tomorrow would have yesterday's archives resolved into it,
// where S3 answers a delete of a missing key with success. The row would be
// dropped and our object orphaned with nothing left pointing at it.
func TestResolveRunStorageWithoutARecordUsesThePlatformDefault(t *testing.T) {
	s := fakeBackupStorageStore{
		def:     &models.BackupStorage{ID: 1, Name: "platform"},
		userDef: map[string]*models.BackupStorage{"u1": ownedBy(5, "u1")},
	}
	got, err := ResolveRunStorage(s, &models.BackupRun{ID: 42}, nil, "u1")
	if err != nil || got.ID != 1 {
		t.Errorf("got %v, %v; want the platform default (1), never the tenant's", got, err)
	}
}
