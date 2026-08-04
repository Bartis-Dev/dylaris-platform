package services

import (
	"database/sql"
	"errors"
	"testing"

	"dylaris-core/store"
)

// maintFakeStore records maintenance setting writes and can fail a chosen key.
type maintFakeStore struct {
	store.Store
	settings map[string]string
	getErr   error
	failSet  map[string]error // key -> error to return from SetSettingBy
	writes   []string         // key=value, in order
}

func newMaintFakeStore() *maintFakeStore {
	return &maintFakeStore{settings: map[string]string{}, failSet: map[string]error{}}
}

func (f *maintFakeStore) GetSetting(key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.settings[key]
	if !ok {
		return "", sql.ErrNoRows
	}
	return v, nil
}

func (f *maintFakeStore) SetSettingBy(key, value, _ string) error {
	if err := f.failSet[key]; err != nil {
		return err
	}
	f.settings[key] = value
	f.writes = append(f.writes, key+"="+value)
	return nil
}

// TestEnableMaintenance pins the contract the migration job depends on: it must
// report whether the platform is ACTUALLY blocked.
//
// Every write here used to be discarded, so a failure still produced a job
// recorded as MaintenanceOn:true, a log line claiming "Maintenance mode enabled
// (block_all)", and a database copy running while users kept writing.
func TestEnableMaintenance(t *testing.T) {
	boom := errors.New("settings table unavailable")

	t.Run("no previous level - the first-run case is not a failure", func(t *testing.T) {
		fs := newMaintFakeStore()
		s := &DBMigrationService{store: fs}

		prev, err := s.enableMaintenance("admin")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prev != "" {
			t.Errorf("prevBlockLevel = %q, want empty", prev)
		}
		if fs.settings["maintenance.active"] != "true" || fs.settings["maintenance.block_level"] != "block_all" {
			t.Errorf("settings = %v, want block_all + active", fs.settings)
		}
	})

	t.Run("previous level is returned so it can be restored", func(t *testing.T) {
		fs := newMaintFakeStore()
		fs.settings["maintenance.block_level"] = "block_writes"
		s := &DBMigrationService{store: fs}

		prev, err := s.enableMaintenance("admin")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prev != "block_writes" {
			t.Errorf("prevBlockLevel = %q, want block_writes", prev)
		}
	})

	t.Run("a failed read is an error, unlike a missing row", func(t *testing.T) {
		fs := newMaintFakeStore()
		fs.getErr = boom
		s := &DBMigrationService{store: fs}

		if _, err := s.enableMaintenance("admin"); err == nil {
			t.Fatal("expected an error when the current block level cannot be read")
		}
	})

	t.Run("a failed activation reports AND rolls the level back", func(t *testing.T) {
		fs := newMaintFakeStore()
		fs.settings["maintenance.block_level"] = "block_writes"
		fs.failSet["maintenance.active"] = boom
		s := &DBMigrationService{store: fs}

		if _, err := s.enableMaintenance("admin"); err == nil {
			t.Fatal("expected an error when maintenance cannot be activated")
		}
		// Leaving block_all applied with maintenance inactive is a state nobody
		// asked for and nothing else clears.
		if got := fs.settings["maintenance.block_level"]; got != "block_writes" {
			t.Errorf("block_level = %q after a failed activation, want the previous value restored", got)
		}
	})

	t.Run("a failed block-level write reports", func(t *testing.T) {
		fs := newMaintFakeStore()
		fs.failSet["maintenance.block_level"] = boom
		s := &DBMigrationService{store: fs}

		if _, err := s.enableMaintenance("admin"); err == nil {
			t.Fatal("expected an error when the block level cannot be raised")
		}
		if fs.settings["maintenance.active"] == "true" {
			t.Error("maintenance was activated even though the block level was never raised")
		}
	})
}

// TestDisableMaintenance covers the other end. It cannot abort anything - the
// migration is over - but a lost write leaves the whole platform in block_all,
// so the behaviour worth pinning is that it still restores what it can.
func TestDisableMaintenance(t *testing.T) {
	t.Run("lifts the block and restores the previous level", func(t *testing.T) {
		fs := newMaintFakeStore()
		s := &DBMigrationService{store: fs}

		s.disableMaintenance("admin", "block_writes")

		if fs.settings["maintenance.active"] != "false" {
			t.Errorf("maintenance.active = %q, want false", fs.settings["maintenance.active"])
		}
		if fs.settings["maintenance.block_level"] != "block_writes" {
			t.Errorf("block_level = %q, want block_writes restored", fs.settings["maintenance.block_level"])
		}
	})

	t.Run("no previous level means nothing to restore", func(t *testing.T) {
		fs := newMaintFakeStore()
		s := &DBMigrationService{store: fs}

		s.disableMaintenance("admin", "")

		if _, ok := fs.settings["maintenance.block_level"]; ok {
			t.Errorf("block_level was written despite there being no previous value: %v", fs.settings)
		}
	})

	t.Run("a failed lift still attempts the level restore", func(t *testing.T) {
		fs := newMaintFakeStore()
		fs.failSet["maintenance.active"] = errors.New("settings table unavailable")
		s := &DBMigrationService{store: fs}

		s.disableMaintenance("admin", "block_writes")

		// One failure must not skip the other write - they are independent, and
		// leaving block_all applied on top of a stuck flag is strictly worse.
		if fs.settings["maintenance.block_level"] != "block_writes" {
			t.Errorf("block_level = %q, want the restore attempted anyway", fs.settings["maintenance.block_level"])
		}
	})
}
