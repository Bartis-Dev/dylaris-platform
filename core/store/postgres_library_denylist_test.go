package store

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestListDisabledLibraryPaths_TruncatedReadIsAnError covers the fail-open half
// of the library denylist.
//
// lib/pq streams a result set, so a connection reset partway through iteration
// ends rows.Next() early and reports itself only via rows.Err(). With that
// check missing the function returned the rows it had managed to read together
// with a nil error - indistinguishable from "these are all the disabled paths".
// The caller turns that list into a lookup set, so every path the read never
// reached became visible to non-admins.
func TestListDisabledLibraryPaths_TruncatedReadIsAnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	rows := sqlmock.NewRows([]string{"path"}).
		AddRow("secret-mods").
		AddRow("staff-only").
		RowError(1, errors.New("connection reset by peer"))
	mock.ExpectQuery(`SELECT path FROM library_disabled`).WillReturnRows(rows)

	paths, err := s.ListDisabledLibraryPaths()
	if err == nil {
		t.Fatalf("got (%v, nil), want an error: a partial denylist read must not "+
			"look like a complete one", paths)
	}
	if paths != nil {
		t.Errorf("paths = %v, want nil - a partial denylist must not be usable", paths)
	}
}

// TestListDisabledLibraryPaths_ScanErrorIsAnError pins the second silent-drop
// route: a row that fails to scan used to `continue`, quietly un-blocking that
// one path while the call still reported success.
func TestListDisabledLibraryPaths_ScanErrorIsAnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	// A NULL path fails to scan into a plain string.
	rows := sqlmock.NewRows([]string{"path"}).
		AddRow("secret-mods").
		AddRow(nil)
	mock.ExpectQuery(`SELECT path FROM library_disabled`).WillReturnRows(rows)

	paths, err := s.ListDisabledLibraryPaths()
	if err == nil {
		t.Fatalf("got (%v, nil), want an error: an unscannable row must not "+
			"silently un-block its path", paths)
	}
}

// TestListDisabledLibraryPaths_HappyPath is the control: the fix must not turn
// an ordinary complete read into an error.
func TestListDisabledLibraryPaths_HappyPath(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	rows := sqlmock.NewRows([]string{"path"}).
		AddRow("secret-mods").
		AddRow("staff-only")
	mock.ExpectQuery(`SELECT path FROM library_disabled`).WillReturnRows(rows)

	paths, err := s.ListDisabledLibraryPaths()
	if err != nil {
		t.Fatalf("ListDisabledLibraryPaths: %v", err)
	}
	if len(paths) != 2 || paths[0] != "secret-mods" || paths[1] != "staff-only" {
		t.Errorf("paths = %v, want [secret-mods staff-only]", paths)
	}
}
