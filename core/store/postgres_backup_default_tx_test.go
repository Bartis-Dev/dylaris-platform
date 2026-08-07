package store

import (
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"dylaris-core/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

// Marking a backup storage default clears the previous default first. That
// clear used to run as its own statement, outside any transaction and with its
// error dropped, so a rejected insert/update left the platform with NO default
// while the request reported failure - and GetDefaultBackupStorage then falls
// back to the lowest-id storage, silently sending scheduled backups elsewhere.
// Live-reproduced: POST a duplicate name with isDefault, the request 500s and
// the existing default's is_default flips to false.
//
// The reachable trigger is the UNIQUE(name) violation, so these pin both halves:
// the failure rolls back, and it surfaces as ErrNameTaken (409) rather than the
// driver's message (500), which spelled out the table and the constraint.

var (
	clearAllDefaults   = regexp.QuoteMeta(`UPDATE backup_storages SET is_default = FALSE WHERE is_default = TRUE`)
	clearOtherDefaults = regexp.QuoteMeta(`UPDATE backup_storages SET is_default = FALSE WHERE is_default = TRUE AND id != $1`)
	insertStorage      = regexp.QuoteMeta(`INSERT INTO backup_storages`)
	updateStorage      = regexp.QuoteMeta(`UPDATE backup_storages SET name = $1`)
)

func defaultNodeLocalStorage() *models.BackupStorage {
	return &models.BackupStorage{
		ID:        7,
		Name:      "Node-local backups",
		Provider:  "node-local",
		Config:    []byte(`{"basePath":"/var/lib/dylaris/backups"}`),
		IsDefault: true,
	}
}

func newMockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	return NewPostgresStore(db), mock, db
}

func TestCreateBackupStorageRollsBackTheClearedDefaultOnDuplicateName(t *testing.T) {
	s, mock, db := newMockStore(t)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec(clearAllDefaults).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(insertStorage).WillReturnError(&pq.Error{Code: "23505"})
	// The whole point: without this the clear above would have been committed
	// on its own and the platform would be left with no default.
	mock.ExpectRollback()

	_, err := s.CreateBackupStorage(defaultNodeLocalStorage())
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken (the handler maps it to 409)", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("the cleared default was not rolled back: %v", err)
	}
}

func TestUpdateBackupStorageRollsBackTheClearedDefaultOnDuplicateName(t *testing.T) {
	s, mock, db := newMockStore(t)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec(clearOtherDefaults).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(updateStorage).WillReturnError(&pq.Error{Code: "23505"})
	mock.ExpectRollback()

	err := s.UpdateBackupStorage(defaultNodeLocalStorage())
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("the cleared default was not rolled back: %v", err)
	}
}

// A PATCH against an id that does not exist used to answer 200 success: the
// UPDATE matched no row and the store reported nil. With isDefault set it was
// worse than cosmetic - the clear ran, so editing a storage that was already
// gone silently unset the real default.
func TestUpdateBackupStorageReportsNotFoundAndKeepsTheDefault(t *testing.T) {
	s, mock, db := newMockStore(t)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec(clearOtherDefaults).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(updateStorage).WillReturnResult(sqlmock.NewResult(0, 0)) // ghost id
	mock.ExpectRollback()

	err := s.UpdateBackupStorage(defaultNodeLocalStorage())
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows (the handler maps it to 404)", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("a ghost-id update did not roll back the cleared default: %v", err)
	}
}

// The success path must still commit, or the fix would trade one silent
// data-loss for another.
func TestCreateBackupStorageCommitsOnSuccess(t *testing.T) {
	s, mock, db := newMockStore(t)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec(clearAllDefaults).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(insertStorage).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectCommit()

	id, err := s.CreateBackupStorage(defaultNodeLocalStorage())
	if err != nil {
		t.Fatalf("CreateBackupStorage: %v", err)
	}
	if id != 7 {
		t.Errorf("id = %d, want 7", id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("the successful create did not commit: %v", err)
	}
}

func TestUpdateBackupStorageCommitsOnSuccess(t *testing.T) {
	s, mock, db := newMockStore(t)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec(clearOtherDefaults).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(updateStorage).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := s.UpdateBackupStorage(defaultNodeLocalStorage()); err != nil {
		t.Fatalf("UpdateBackupStorage: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("the successful update did not commit: %v", err)
	}
}

// A create that does NOT claim the default must not touch any other row, so the
// transaction carries the insert alone.
func TestCreateBackupStorageWithoutDefaultTouchesNoOtherRow(t *testing.T) {
	s, mock, db := newMockStore(t)
	defer db.Close()

	bs := defaultNodeLocalStorage()
	bs.IsDefault = false

	mock.ExpectBegin()
	mock.ExpectQuery(insertStorage).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))
	mock.ExpectCommit()

	if _, err := s.CreateBackupStorage(bs); err != nil {
		t.Fatalf("CreateBackupStorage: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("a non-default create ran an unexpected statement: %v", err)
	}
}

// The storage connections have no default flag, so only the error mapping is at
// stake there: a duplicate name has to reach the handler as ErrNameTaken and a
// ghost id as sql.ErrNoRows, instead of the driver's message at 500 and a
// fabricated success.
func TestStorageConnectionDuplicateNameSurfacesAsErrNameTaken(t *testing.T) {
	s, mock, db := newMockStore(t)
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO storage_connections`)).
		WillReturnError(&pq.Error{Code: "23505"})

	_, err := s.CreateStorageConnection(&models.StorageConnection{Name: "taken", Provider: "s3"})
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestUpdateStorageConnectionReportsNotFound(t *testing.T) {
	s, mock, db := newMockStore(t)
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE storage_connections SET name = $1`)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := s.UpdateStorageConnection(&models.StorageConnection{ID: 9999, Name: "ghost", Provider: "s3"})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// isUniqueViolation must key on the SQLSTATE, not on the message text, which
// carries the table and constraint names and changes with the schema.
func TestIsUniqueViolationMatchesOnSQLState(t *testing.T) {
	if !isUniqueViolation(&pq.Error{Code: "23505", Message: "anything"}) {
		t.Error("a 23505 was not recognized")
	}
	if isUniqueViolation(&pq.Error{Code: "23503", Message: "duplicate key value violates unique constraint"}) {
		t.Error("a foreign-key violation was misread as a duplicate name")
	}
	if isUniqueViolation(errors.New("duplicate key value violates unique constraint")) {
		t.Error("a plain error carrying the same words was misread as a duplicate name")
	}
}
