package store

import (
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreateServerRole_ReturnsID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	owner := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(regexp.QuoteMeta(
		`INSERT INTO server_roles (owner_user_id, name, capabilities) VALUES ($1, $2, $3::jsonb) RETURNING id`)).
		WithArgs(owner, "Builders", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(12))

	id, err := s.CreateServerRole(owner, "Builders", []string{"files.read", "console.read"})
	if err != nil {
		t.Fatalf("CreateServerRole: %v", err)
	}
	if id != 12 {
		t.Fatalf("got id %d, want 12", id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestListServerRolesByOwner_ParsesRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	owner := "11111111-1111-1111-1111-111111111111"
	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "owner_user_id", "name", "capabilities", "created_at"}).
		AddRow(1, owner, "Builders", []byte(`["files.read","files.write"]`), now).
		AddRow(2, owner, "Ops", []byte(`["power.start"]`), now)
	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT id, owner_user_id, name, COALESCE(capabilities, '[]'::jsonb), created_at FROM server_roles WHERE owner_user_id = $1 ORDER BY name ASC`)).
		WithArgs(owner).
		WillReturnRows(rows)

	got, err := s.ListServerRolesByOwner(owner)
	if err != nil {
		t.Fatalf("ListServerRolesByOwner: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d roles, want 2", len(got))
	}
	if got[0].Name != "Builders" || len(got[0].Capabilities) != 2 || got[0].OwnerUserID != owner {
		t.Fatalf("row 0 unexpected: %+v", got[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestUpdateServerRole_NoRowReturnsErrNoRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	owner := "11111111-1111-1111-1111-111111111111"
	mock.ExpectExec(regexp.QuoteMeta(
		`UPDATE server_roles SET name = $3, capabilities = $4::jsonb WHERE id = $1 AND owner_user_id = $2`)).
		WithArgs(9, owner, "x", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = s.UpdateServerRole(9, owner, "x", []string{})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("got %v, want sql.ErrNoRows", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestDeleteServerRole_ScopedByOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	owner := "11111111-1111-1111-1111-111111111111"
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM server_roles WHERE id = $1 AND owner_user_id = $2`)).
		WithArgs(3, owner).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.DeleteServerRole(3, owner); err != nil {
		t.Fatalf("DeleteServerRole: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
