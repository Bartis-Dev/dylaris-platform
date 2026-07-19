package database

import (
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestApplyStorageManifestsSchema_IsAdditiveAndIdempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS storage_manifests")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS storage_manifest_entries")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := applyStorageManifestsSchema(db); err != nil {
		t.Fatalf("applyStorageManifestsSchema: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestApplyStorageManifestsSchema_DropsNothing(t *testing.T) {
	// The two tables are ADDITIVE. Any DROP/TRUNCATE/DELETE in this phase
	// would silently destroy operator data on the next boot.
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	for i := 0; i < 2; i++ {
		mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
	}
	if err := applyStorageManifestsSchema(db); err != nil {
		t.Fatalf("applyStorageManifestsSchema: %v", err)
	}
	// Asserts the phase issues exactly the two expected execs (no more, no
	// fewer) - a regression that adds or drops a statement fails here.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	// sqlmock does not hand back the SQL text, so the destructive-statement
	// check runs against the source of truth instead: the statements this
	// phase is allowed to issue.
	for _, forbidden := range []string{"DROP ", "TRUNCATE ", "DELETE FROM "} {
		if strings.Contains(strings.ToUpper(storageManifestsSchemaSQL()), forbidden) {
			t.Errorf("storage-manifests phase contains a destructive statement (%s)", strings.TrimSpace(forbidden))
		}
	}
}

func TestApplyStorageManifestsSchema_PropagatesError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS storage_manifests")).
		WillReturnError(errBoom)

	if err := applyStorageManifestsSchema(db); err == nil {
		t.Fatal("applyStorageManifestsSchema err = nil, want the exec error wrapped")
	}
}

// errBoom is a local sentinel for the propagation test.
var errBoom = errBoomType{}

type errBoomType struct{}

func (errBoomType) Error() string { return "boom" }
