package store

import (
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestUpsertServerGrant_AccountWide(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	owner := "11111111-1111-1111-1111-111111111111"
	friend := "22222222-2222-2222-2222-222222222222"
	roleID := 7
	mock.ExpectExec(regexp.QuoteMeta(
		`INSERT INTO server_invites (server_id, user_id, owner_user_id, server_role_id, cap_overrides, inherit, invited_by) VALUES (NULL, $1, $2, $3, $4::jsonb, $5, $2) ON CONFLICT (owner_user_id, user_id) WHERE server_id IS NULL DO UPDATE SET server_role_id = EXCLUDED.server_role_id, cap_overrides = EXCLUDED.cap_overrides, inherit = EXCLUDED.inherit`)).
		WithArgs(friend, owner, roleID, sqlmock.AnyArg(), false).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = s.UpsertServerGrant(nil, friend, owner, &roleID, CapOverrides{Grant: []string{"modpack.read"}}, false)
	if err != nil {
		t.Fatalf("UpsertServerGrant account-wide: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestUpsertServerGrant_PerServer(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	owner := "11111111-1111-1111-1111-111111111111"
	friend := "22222222-2222-2222-2222-222222222222"
	serverID := 42
	roleID := 7
	mock.ExpectExec(regexp.QuoteMeta(
		`INSERT INTO server_invites (server_id, user_id, owner_user_id, server_role_id, cap_overrides, inherit, invited_by) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $3) ON CONFLICT (server_id, user_id) DO UPDATE SET owner_user_id = EXCLUDED.owner_user_id, server_role_id = EXCLUDED.server_role_id, cap_overrides = EXCLUDED.cap_overrides, inherit = EXCLUDED.inherit`)).
		WithArgs(serverID, friend, owner, roleID, sqlmock.AnyArg(), true).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = s.UpsertServerGrant(&serverID, friend, owner, &roleID, CapOverrides{Grant: []string{"files.read"}}, true)
	if err != nil {
		t.Fatalf("UpsertServerGrant per-server: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestDeleteServerGrant_AccountWide(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	owner := "11111111-1111-1111-1111-111111111111"
	friend := "22222222-2222-2222-2222-222222222222"
	mock.ExpectExec(regexp.QuoteMeta(
		`DELETE FROM server_invites WHERE server_id IS NULL AND owner_user_id = $1 AND user_id = $2`)).
		WithArgs(owner, friend).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.DeleteServerGrant(nil, owner, friend); err != nil {
		t.Fatalf("DeleteServerGrant account-wide: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestDeleteServerGrant_PerServerNoRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	friend := "22222222-2222-2222-2222-222222222222"
	serverID := 42
	mock.ExpectExec(regexp.QuoteMeta(
		`DELETE FROM server_invites WHERE server_id = $1 AND user_id = $2`)).
		WithArgs(serverID, friend).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = s.DeleteServerGrant(&serverID, "unused-owner", friend)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("got %v, want sql.ErrNoRows", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
