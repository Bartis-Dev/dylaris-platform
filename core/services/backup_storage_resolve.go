package services

import (
	"errors"

	"dylaris-core/models"
)

// ErrNoBackupStorage means neither the job nor the platform has a storage to
// use. It is the only case where refusing is correct.
var ErrNoBackupStorage = errors.New("no backup storage available")

// backupStorageLookup is the narrow port ResolveJobStorage needs, satisfied by
// *store.PostgresStore and by the scheduler's own store.
type backupStorageLookup interface {
	GetBackupStorage(id int) (*models.BackupStorage, error)
	GetDefaultBackupStorage() (*models.BackupStorage, error)
}

// ResolveJobStorage returns the storage a backup job uses: its own when one is
// set, otherwise the platform default.
//
// This exists because the two halves disagreed. The run path already had the
// fallback and named it - "Resolve storage (job-level or fallback to default)" -
// while restore, download and the archive verification each did
// `if job.StorageID == nil { refuse }`. The panel's storage dropdown offers
// "Default storage" as its FIRST option, which stores NULL, so a job created by
// not choosing backed up perfectly and then could not be restored, downloaded,
// or even checked for whether its archive existed.
//
// Backups that cannot be restored are worse than no backups: the failure only
// surfaces when someone is already trying to recover.
func ResolveJobStorage(s backupStorageLookup, storageID *int) (*models.BackupStorage, error) {
	if storageID != nil {
		bs, err := s.GetBackupStorage(*storageID)
		if err != nil {
			return nil, err
		}
		if bs == nil {
			return nil, ErrNoBackupStorage
		}
		return bs, nil
	}
	bs, err := s.GetDefaultBackupStorage()
	if err != nil {
		return nil, err
	}
	if bs == nil {
		return nil, ErrNoBackupStorage
	}
	return bs, nil
}
