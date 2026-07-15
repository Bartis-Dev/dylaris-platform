package database

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestApplyAuthzGrantsSchema_PartialIndex pins the F6 partial unique index: it
// must be scoped WHERE server_id IS NULL so only account-wide grants are
// de-duplicated (legacy per-server rows keep relying on UNIQUE(server_id,
// user_id)). IF NOT EXISTS keeps a re-boot a no-op.
func TestApplyAuthzGrantsSchema_PartialIndex(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta(
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_server_invites_account_grant ON server_invites (owner_user_id, user_id) WHERE server_id IS NULL`)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := applyAuthzGrantsSchema(db); err != nil {
		t.Fatalf("applyAuthzGrantsSchema: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
