package database

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"
)

// TestEnsureSchemaExclusive_ConcurrentBootstrap is the regression test for
// `deploy: mode: global` on Core: several replicas boot at the same instant and
// every one of them runs the schema bootstrap against the same empty database.
//
// Without the advisory lock this fails the way concurrent DDL always does -
// CREATE TABLE IF NOT EXISTS checks existence and inserts non-atomically, so a
// loser collides on pg_type's unique index. It is timing-dependent, so this test
// is not a guarantee the bug is gone; it reproduced reliably enough to be worth
// keeping, and it can only fail when something is actually wrong.
func TestEnsureSchemaExclusive_ConcurrentBootstrap(t *testing.T) {
	cfg := testDBConfig(t) // skips unless DYLARIS_TEST_DB_HOST is set

	// A scratch database, so this races on a genuinely empty schema rather than
	// on tables an earlier test already created (which would make every
	// statement a cheap no-op and hide the race entirely).
	admin, err := sql.Open("postgres", fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, testDBName()))
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	defer admin.Close()

	dbName := uniqueName("schemalock_")
	if _, err := admin.Exec(`CREATE DATABASE ` + dbName); err != nil {
		t.Fatalf("create scratch database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + dbName + ` WITH (FORCE)`); err != nil {
			t.Logf("drop scratch database %s: %v", dbName, err)
		}
	})

	const replicas = 4
	open := func() *sql.DB {
		db, err := sql.Open("postgres", fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, dbName))
		if err != nil {
			t.Fatalf("open replica connection: %v", err)
		}
		return db
	}

	// Release all four at once: staggered starts are exactly the case that does
	// NOT reproduce this.
	var start, done sync.WaitGroup
	start.Add(1)
	errs := make([]error, replicas)
	for i := 0; i < replicas; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			db := open()
			defer db.Close()
			start.Wait()
			errs[i] = ensureSchemaExclusive(db, false)
		}(i)
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("replica %d failed to bootstrap the schema concurrently: %v", i, err)
		}
	}

	// The schema must also be complete afterwards, not merely error-free: a lock
	// that serialised the calls but left a half-built schema would still be a bug.
	db := open()
	defer db.Close()
	for _, table := range []string{"users", "servers", "nodes", "settings"} {
		var exists bool
		if err := db.QueryRow(
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'public' AND table_name = $1)`,
			table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %q missing after concurrent bootstrap", table)
		}
	}
}
