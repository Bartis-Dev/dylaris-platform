package store

import (
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreatePanelRole_ReturnsID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	uid := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(regexp.QuoteMeta(
		`INSERT INTO panel_roles (name, capabilities, is_system, created_by) VALUES ($1, $2::jsonb, FALSE, $3) RETURNING id`)).
		WithArgs("Support L1", sqlmock.AnyArg(), uid).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))

	id, err := s.CreatePanelRole("Support L1", []string{"tickets.read", "users.read"}, &uid)
	if err != nil {
		t.Fatalf("CreatePanelRole: %v", err)
	}
	if id != 7 {
		t.Fatalf("got id %d, want 7", id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestListPanelRoles_ParsesRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "name", "capabilities", "is_system", "created_by", "created_at"}).
		AddRow(1, "admin", []byte(`["users.read","users.write"]`), true, nil, now).
		AddRow(2, "support", []byte(`["tickets.read"]`), true, nil, now)
	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT id, name, COALESCE(capabilities, '[]'::jsonb), is_system, created_by, created_at FROM panel_roles ORDER BY is_system DESC, name ASC`)).
		WillReturnRows(rows)

	got, err := s.ListPanelRoles()
	if err != nil {
		t.Fatalf("ListPanelRoles: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d roles, want 2", len(got))
	}
	if got[0].Name != "admin" || len(got[0].Capabilities) != 2 || !got[0].IsSystem {
		t.Fatalf("row 0 unexpected: %+v", got[0])
	}
	if got[0].CreatedBy != nil {
		t.Fatalf("expected nil CreatedBy, got %v", *got[0].CreatedBy)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestUpdatePanelRole_NoRowReturnsErrNoRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	mock.ExpectExec(regexp.QuoteMeta(
		`UPDATE panel_roles SET name = $2, capabilities = $3::jsonb WHERE id = $1`)).
		WithArgs(9, "x", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = s.UpdatePanelRole(9, "x", []string{})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("got %v, want sql.ErrNoRows", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestDeletePanelRole_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM panel_roles WHERE id = $1`)).
		WithArgs(3).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.DeletePanelRole(3); err != nil {
		t.Fatalf("DeletePanelRole: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestSetUserPanelRole_AssignsID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	uid := "11111111-1111-1111-1111-111111111111"
	roleID := 5
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE users SET panel_role_id = $1 WHERE id = $2`)).
		WithArgs(roleID, uid).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.SetUserPanelRole(uid, &roleID); err != nil {
		t.Fatalf("SetUserPanelRole: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestSetUserPanelCapOverrides_Marshals(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	uid := "11111111-1111-1111-1111-111111111111"
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE users SET panel_cap_overrides = $1::jsonb WHERE id = $2`)).
		WithArgs(sqlmock.AnyArg(), uid).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.SetUserPanelCapOverrides(uid, CapOverrides{Grant: []string{"nodes.read"}, Deny: []string{"users.delete"}}); err != nil {
		t.Fatalf("SetUserPanelCapOverrides: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
