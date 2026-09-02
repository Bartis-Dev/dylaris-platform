package services

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// Values are QUOTED as of 2026-09-03. This test pinned the unquoted form, which
// was the form that let an empty or space-containing password swallow the
// keyword after it - see TestADSNValueCannotSwallowTheNextField for the two
// failures that caused, one of them in production. The expectation moved
// because the meaning did.
func TestDBConnParamsDSN(t *testing.T) {
	p := DBConnParams{Host: "h", Port: "5432", User: "u", Password: "p", DBName: "d"}
	got := p.DSN()
	want := "host='h' port='5432' user='u' password='p' dbname='d' sslmode='disable'"
	if got != want {
		t.Fatalf("DSN()\n got: %q\nwant: %q", got, want)
	}
	p.SSLMode = "require"
	if got := p.DSN(); got != "host='h' port='5432' user='u' password='p' dbname='d' sslmode='require'" {
		t.Fatalf("DSN() with sslmode: %q", got)
	}
}

func TestTopoSortTables_ParentsBeforeChildren(t *testing.T) {
	tables := []string{"server_invites", "servers", "users"}
	deps := map[string][]string{
		"servers":        {"users"},
		"server_invites": {"servers", "users"},
	}
	got := topoSortTables(tables, deps)
	want := []string{"users", "servers", "server_invites"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("topoSortTables()\n got: %v\nwant: %v", got, want)
	}
}

func TestTopoSortTables_IgnoresSelfRefAndUnknownParents(t *testing.T) {
	tables := []string{"servers", "users"}
	deps := map[string][]string{
		"servers": {"servers", "users", "nonexistent_table"}, // self-ref + missing parent
	}
	got := topoSortTables(tables, deps)
	want := []string{"users", "servers"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("topoSortTables()\n got: %v\nwant: %v", got, want)
	}
}

func TestTopoSortTables_CycleLeftoverAppended(t *testing.T) {
	// Mutual FK (a<->b): no valid topo order; both must still appear, deterministically.
	tables := []string{"b", "a", "c"}
	deps := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	got := topoSortTables(tables, deps)
	if len(got) != 3 {
		t.Fatalf("expected all 3 tables, got %v", got)
	}
	// "c" has no deps so it comes first; a,b are the cycle leftover (alpha order).
	want := []string{"c", "a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("topoSortTables()\n got: %v\nwant: %v", got, want)
	}
}

func TestBuildInsertPrefix(t *testing.T) {
	got := buildInsertPrefix("server_stats", []string{"time", "server_id"})
	want := `INSERT INTO "server_stats" ("time", "server_id") VALUES `
	if got != want {
		t.Fatalf("buildInsertPrefix()\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildValuePlaceholders(t *testing.T) {
	got := buildValuePlaceholders(2, 3)
	want := "($1, $2, $3), ($4, $5, $6)"
	if got != want {
		t.Fatalf("buildValuePlaceholders()\n got: %q\nwant: %q", got, want)
	}
}

func TestQuoteIdent(t *testing.T) {
	if got := quoteIdent(`weird"name`); got != `"weird""name"` {
		t.Fatalf("quoteIdent() = %q", got)
	}
}

func TestDiscoverTablesOrdered(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("FROM pg_tables").WillReturnRows(
		sqlmock.NewRows([]string{"tablename"}).
			AddRow("server_invites").
			AddRow("servers").
			AddRow("users"),
	)
	mock.ExpectQuery("FOREIGN KEY").WillReturnRows(
		sqlmock.NewRows([]string{"child", "parent"}).
			AddRow("servers", "users").
			AddRow("server_invites", "servers").
			AddRow("server_invites", "users"),
	)

	got, err := discoverTablesOrdered(context.Background(), db)
	if err != nil {
		t.Fatalf("discoverTablesOrdered: %v", err)
	}
	want := []string{"users", "servers", "server_invites"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order\n got: %v\nwant: %v", got, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestTableColumns(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("information_schema.columns").
		WithArgs("servers").
		WillReturnRows(sqlmock.NewRows([]string{"column_name"}).
			AddRow("id").
			AddRow("name").
			AddRow("owner_id"))

	got, err := tableColumns(context.Background(), db, "servers")
	if err != nil {
		t.Fatalf("tableColumns: %v", err)
	}
	want := []string{"id", "name", "owner_id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("columns\n got: %v\nwant: %v", got, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCopyTableText(t *testing.T) {
	srcDB, srcMock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New src: %v", err)
	}
	defer srcDB.Close()
	dstDB, dstMock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New dst: %v", err)
	}
	defer dstDB.Close()

	cols := []string{"id", "label"}

	// Source read: every column cast to text; one row has a NULL label.
	srcMock.ExpectQuery(`SELECT "id"::text, "label"::text FROM "widgets"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "label"}).
			AddRow("1", "alpha").
			AddRow("2", nil))

	// Target insert: single batched INSERT with 2 value tuples (one batch).
	dstMock.ExpectExec(`INSERT INTO "widgets"`).
		WithArgs("1", "alpha", "2", nil).
		WillReturnResult(sqlmock.NewResult(0, 2))

	n, err := copyTableText(context.Background(), srcDB, dstDB, "widgets", cols)
	if err != nil {
		t.Fatalf("copyTableText: %v", err)
	}
	if n != 2 {
		t.Fatalf("rows copied = %d, want 2", n)
	}
	if err := srcMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("src unmet: %v", err)
	}
	if err := dstMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("dst unmet: %v", err)
	}
}

func verifyTime() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

func TestVerifyCopy_AllMatch(t *testing.T) {
	srcDB, srcMock, _ := sqlmock.New()
	defer srcDB.Close()
	dstDB, dstMock, _ := sqlmock.New()
	defer dstDB.Close()

	srcMock.ExpectQuery("FROM pg_tables").WillReturnRows(
		sqlmock.NewRows([]string{"tablename"}).AddRow("servers").AddRow("users"))
	dstMock.ExpectQuery("FROM pg_tables").WillReturnRows(
		sqlmock.NewRows([]string{"tablename"}).AddRow("servers").AddRow("users"))

	// Union sorted = [servers, users]; per table: source count then target count.
	srcMock.ExpectQuery(`FROM "servers"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(5))
	dstMock.ExpectQuery(`FROM "servers"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(5))
	srcMock.ExpectQuery(`FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))
	dstMock.ExpectQuery(`FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))

	rep, err := VerifyCopy(context.Background(), srcDB, dstDB, verifyTime())
	if err != nil {
		t.Fatalf("VerifyCopy: %v", err)
	}
	if !rep.OK {
		t.Fatalf("expected OK, got log: %v", rep.Log)
	}
	if len(rep.Tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(rep.Tables))
	}
	if err := srcMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("src unmet: %v", err)
	}
	if err := dstMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("dst unmet: %v", err)
	}
}

func TestVerifyCopy_RowCountMismatch(t *testing.T) {
	srcDB, srcMock, _ := sqlmock.New()
	defer srcDB.Close()
	dstDB, dstMock, _ := sqlmock.New()
	defer dstDB.Close()

	srcMock.ExpectQuery("FROM pg_tables").WillReturnRows(
		sqlmock.NewRows([]string{"tablename"}).AddRow("servers"))
	dstMock.ExpectQuery("FROM pg_tables").WillReturnRows(
		sqlmock.NewRows([]string{"tablename"}).AddRow("servers"))
	srcMock.ExpectQuery(`FROM "servers"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(5))
	dstMock.ExpectQuery(`FROM "servers"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(4))

	rep, err := VerifyCopy(context.Background(), srcDB, dstDB, verifyTime())
	if err != nil {
		t.Fatalf("VerifyCopy: %v", err)
	}
	if rep.OK {
		t.Fatalf("expected NOT OK on row mismatch")
	}
	if rep.Tables[0].OK {
		t.Fatalf("servers should be flagged not-ok")
	}
}

func TestVerifyCopy_MissingOnTarget(t *testing.T) {
	srcDB, srcMock, _ := sqlmock.New()
	defer srcDB.Close()
	dstDB, dstMock, _ := sqlmock.New()
	defer dstDB.Close()

	srcMock.ExpectQuery("FROM pg_tables").WillReturnRows(
		sqlmock.NewRows([]string{"tablename"}).AddRow("servers").AddRow("users"))
	dstMock.ExpectQuery("FROM pg_tables").WillReturnRows(
		sqlmock.NewRows([]string{"tablename"}).AddRow("users"))

	// servers exists only on source -> source count queried, no target count.
	srcMock.ExpectQuery(`FROM "servers"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(3))
	srcMock.ExpectQuery(`FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))
	dstMock.ExpectQuery(`FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))

	rep, err := VerifyCopy(context.Background(), srcDB, dstDB, verifyTime())
	if err != nil {
		t.Fatalf("VerifyCopy: %v", err)
	}
	if rep.OK {
		t.Fatalf("expected NOT OK when a table is missing on target")
	}
	var serversTV *TableVerify
	for i := range rep.Tables {
		if rep.Tables[i].Table == "servers" {
			serversTV = &rep.Tables[i]
		}
	}
	if serversTV == nil || serversTV.TargetExists {
		t.Fatalf("servers should be present-on-source, missing-on-target: %+v", serversTV)
	}
}

func TestTruncateAll(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`TRUNCATE TABLE "users", "servers" CASCADE`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := truncateAll(context.Background(), db, []string{"users", "servers"}); err != nil {
		t.Fatalf("truncateAll: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
