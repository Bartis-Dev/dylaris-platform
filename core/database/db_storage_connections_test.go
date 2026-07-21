package database

import (
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestApplyStorageConnectionsSchema_IsAdditiveAndIdempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS storage_connections")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := applyStorageConnectionsSchema(db); err != nil {
		t.Fatalf("applyStorageConnectionsSchema: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestApplyStorageConnectionsSchema_DropsNothing(t *testing.T) {
	// The table is ADDITIVE. Any DROP/TRUNCATE/DELETE in this phase would
	// silently destroy operator-configured connections on the next boot, and
	// EnsureSchema also runs against a fresh migration-target DB.
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := applyStorageConnectionsSchema(db); err != nil {
		t.Fatalf("applyStorageConnectionsSchema: %v", err)
	}
	// Asserts the phase issues exactly the one expected exec (no more, no
	// fewer) - a regression that adds or drops a statement fails here.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	// sqlmock does not hand back the SQL text, so the destructive-statement
	// check runs against the source of truth: the statements this phase issues.
	for _, forbidden := range []string{"DROP ", "TRUNCATE ", "DELETE FROM "} {
		if strings.Contains(strings.ToUpper(storageConnectionsSchemaSQL()), forbidden) {
			t.Errorf("storage-connections phase contains a destructive statement (%s)", strings.TrimSpace(forbidden))
		}
	}
	// The secret must live in its own column, never in the config JSONB.
	if !strings.Contains(storageConnectionsSchemaSQL(), "secret_enc") {
		t.Error("storage-connections table is missing the dedicated secret_enc column")
	}
}

func TestApplyStorageConnectionsSchema_PropagatesError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS storage_connections")).
		WillReturnError(errBoom)

	if err := applyStorageConnectionsSchema(db); err == nil {
		t.Fatal("applyStorageConnectionsSchema err = nil, want the exec error wrapped")
	}
}
