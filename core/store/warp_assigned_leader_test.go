package store

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGetWarpPeerByPubkey_ScansAssignedLeader(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := &PostgresStore{db: db}

	rows := sqlmock.NewRows([]string{"id", "api_key_id", "pubkey", "wg_ip", "region", "created_at", "assigned_leader"}).
		AddRow(7, 3, "PUBKEY", "10.99.1.5", "eu-1", time.Now(), "leader-b")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, api_key_id, pubkey, wg_ip, region, created_at, COALESCE(assigned_leader, '')")).
		WithArgs("PUBKEY").
		WillReturnRows(rows)

	p, err := s.GetWarpPeerByPubkey("PUBKEY")
	if err != nil {
		t.Fatalf("GetWarpPeerByPubkey: %v", err)
	}
	if p.AssignedLeader != "leader-b" {
		t.Fatalf("AssignedLeader = %q, want leader-b", p.AssignedLeader)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestSetWarpPeerAssignedLeader_UpdatesByPubkey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := &PostgresStore{db: db}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE warp_peers SET assigned_leader = $2 WHERE pubkey = $1")).
		WithArgs("PUBKEY", "leader-b").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.SetWarpPeerAssignedLeader("PUBKEY", "leader-b"); err != nil {
		t.Fatalf("SetWarpPeerAssignedLeader: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}
