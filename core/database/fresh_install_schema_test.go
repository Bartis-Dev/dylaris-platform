package database

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every column migrateSchema promises has to exist after ONE boot.
//
// That set runs at position 12 of ensureSchema, before the later phases create
// their own tables, and ADD COLUMN against a table that does not exist yet
// fails. The failure used to be discarded, so the column was simply absent on a
// fresh install and appeared on the next restart - which is why
// modversions.modrinth_download_url shipped missing and every INSERT on that
// table answered 42703 until someone restarted Core.
//
// A column whose table is created later belongs in that table's CREATE TABLE,
// with the ALTER repeated in the same phase for older databases. This test is
// what says so at authoring time rather than after a support ticket.
func TestFreshInstallHasEveryMigratedColumn(t *testing.T) {
	db := freshSchemaDB(t)

	cols := parseMigrateSchemaColumns(t)
	if len(cols) < 40 {
		t.Fatalf("parsed only %d migrated columns - the extraction is broken, not the schema", len(cols))
	}

	var missing []string
	for _, c := range cols {
		var n int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`,
			c.table, c.col).Scan(&n)
		if err != nil {
			t.Fatalf("query information_schema for %s.%s: %v", c.table, c.col, err)
		}
		if n == 0 {
			missing = append(missing, c.table+"."+c.col)
		}
	}
	t.Logf("checked %d migrated columns against a one-boot database", len(cols))
	if len(missing) > 0 {
		t.Errorf("%d column(s) missing after a fresh install: %s\n"+
			"    Their table is created after migrateSchema runs. Declare the column in that "+
			"CREATE TABLE and repeat the ALTER in the same phase.",
			len(missing), strings.Join(missing, ", "))
	}
}

type migratedColumn struct{ table, col string }

// parseMigrateSchemaColumns reads the ADD COLUMN set out of the source. The
// list is a local inside migrateSchema; reading the source keeps the test from
// forcing it into the package's API, the same approach the other SQL guards in
// this repo take.
func parseMigrateSchemaColumns(t *testing.T) []migratedColumn {
	t.Helper()
	src, err := os.ReadFile("db_tables.go")
	if err != nil {
		t.Fatalf("read db_tables.go: %v", err)
	}
	body := string(src)

	start := strings.Index(body, "cols := []struct{ table, col, def string }{")
	if start < 0 {
		t.Fatal("could not find the ADD COLUMN set in db_tables.go - has it been renamed?")
	}
	end := strings.Index(body[start:], "\n\t}")
	if end < 0 {
		t.Fatal("could not find the end of the ADD COLUMN set")
	}

	entry := regexp.MustCompile(`\{"([a-z_0-9]+)",\s*"([a-z_0-9]+)",`)
	var out []migratedColumn
	for _, m := range entry.FindAllStringSubmatch(body[start:start+end], -1) {
		out = append(out, migratedColumn{table: m[1], col: m[2]})
	}
	return out
}
