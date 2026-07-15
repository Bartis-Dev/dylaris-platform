package store

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestEnrollPeerTx_FixedIPTaken pins the race-safe graceful rejection: a caller-pinned
// fixed IP that is already allocated returns ErrWarpIPTaken (not a raw wg_ip UNIQUE
// violation), and no INSERT is attempted.
func TestEnrollPeerTx_FixedIPTaken(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
		WithArgs(warpEnrollLock).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM warp_peers WHERE api_key_id = $1`)).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM warp_peers WHERE wg_ip = $1`)).
		WithArgs("10.0.99.7").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()

	_, _, err = s.EnrollPeerTx(1, 5, "block", "pubA", "10.0.99.7", "leader-01",
		func(map[string]bool) (string, error) { return "", nil })
	if !errors.Is(err, ErrWarpIPTaken) {
		t.Fatalf("EnrollPeerTx with a taken fixed IP: got %v, want ErrWarpIPTaken", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
