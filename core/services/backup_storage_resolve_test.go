package services

import (
	"errors"
	"testing"

	"dylaris-core/models"
)

type fakeBackupStorageStore struct {
	byID    map[int]*models.BackupStorage
	def     *models.BackupStorage
	byIDErr error
	defErr  error
}

func (f fakeBackupStorageStore) GetBackupStorage(id int) (*models.BackupStorage, error) {
	if f.byIDErr != nil {
		return nil, f.byIDErr
	}
	return f.byID[id], nil
}

func (f fakeBackupStorageStore) GetDefaultBackupStorage() (*models.BackupStorage, error) {
	if f.defErr != nil {
		return nil, f.defErr
	}
	return f.def, nil
}

func intPtr(i int) *int { return &i }

// TestResolveJobStorage pins the contract the two halves of the backup feature
// disagreed on. The run path fell back to the default storage when a job had
// none; restore, download and the archive check refused outright. Since the
// panel's storage dropdown offers "Default storage" FIRST and stores NULL, a
// job created by not choosing produced backups that could not be restored,
// downloaded or verified - which is only discovered while recovering.
func TestResolveJobStorage(t *testing.T) {
	own := &models.BackupStorage{ID: 7, Name: "job storage"}
	def := &models.BackupStorage{ID: 1, Name: "default storage"}

	t.Run("job storage wins when set", func(t *testing.T) {
		s := fakeBackupStorageStore{byID: map[int]*models.BackupStorage{7: own}, def: def}
		got, err := ResolveJobStorage(s, intPtr(7))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != 7 {
			t.Errorf("got storage %d, want the job's own (7)", got.ID)
		}
	})

	// The case that shipped broken.
	t.Run("nil storage falls back to the default", func(t *testing.T) {
		s := fakeBackupStorageStore{byID: map[int]*models.BackupStorage{}, def: def}
		got, err := ResolveJobStorage(s, nil)
		if err != nil {
			t.Fatalf("a job on the default storage was refused: %v", err)
		}
		if got.ID != 1 {
			t.Errorf("got storage %d, want the default (1)", got.ID)
		}
	})

	// Refusing IS correct here, and the message has to stay distinguishable
	// from "the id you gave does not resolve".
	t.Run("nil storage and no default is the one real refusal", func(t *testing.T) {
		s := fakeBackupStorageStore{byID: map[int]*models.BackupStorage{}}
		if _, err := ResolveJobStorage(s, nil); !errors.Is(err, ErrNoBackupStorage) {
			t.Errorf("err = %v, want ErrNoBackupStorage", err)
		}
	})

	t.Run("a storage id that no longer exists is not silently defaulted", func(t *testing.T) {
		s := fakeBackupStorageStore{byID: map[int]*models.BackupStorage{}, def: def}
		if _, err := ResolveJobStorage(s, intPtr(99)); !errors.Is(err, ErrNoBackupStorage) {
			t.Errorf("err = %v, want ErrNoBackupStorage", err)
		}
	})

	t.Run("lookup errors propagate", func(t *testing.T) {
		boom := errors.New("db down")
		if _, err := ResolveJobStorage(fakeBackupStorageStore{byIDErr: boom}, intPtr(7)); !errors.Is(err, boom) {
			t.Errorf("by-id err = %v, want the db error", err)
		}
		if _, err := ResolveJobStorage(fakeBackupStorageStore{defErr: boom}, nil); !errors.Is(err, boom) {
			t.Errorf("default err = %v, want the db error", err)
		}
	})
}
