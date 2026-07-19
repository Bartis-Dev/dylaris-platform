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
	mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS idx_storage_manifest_entries_manifest")).
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

	var executed []string
	for i := 0; i < 3; i++ {
		mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
	}
	// sqlmock does not hand back the SQL text, so assert on the source of
	// truth instead: the statements this phase is allowed to issue.
	_ = executed
	if err := applyStorageManifestsSchema(db); err != nil {
		t.Fatalf("applyStorageManifestsSchema: %v", err)
	}
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
