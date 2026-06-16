package services

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"dylaris-core/database"

	_ "github.com/lib/pq" // Postgres driver for the target connection.
)

// DBConnParams holds everything needed to open a Postgres / TimescaleDB
// connection. It is used both for the live SOURCE database (snapshotted from the
// running Core config) and for the migration TARGET (supplied by the admin).
type DBConnParams struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
	// DBType is the backend kind for THIS connection ("timescaledb" | "postgres").
	// It only matters for the target, where it decides whether server_stats is
	// provisioned as a hypertable.
	DBType string
}

// DSN renders a lib/pq connection string. SSLMode defaults to "disable" to match
// the bundled in-Docker database.
func (p DBConnParams) DSN() string {
	ssl := strings.TrimSpace(p.SSLMode)
	if ssl == "" {
		ssl = "disable"
	}
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		p.Host, p.Port, p.User, p.Password, p.DBName, ssl)
}

// Open opens the connection and verifies it answers within the timeout. The
// caller owns the returned *sql.DB and must Close it.
func (p DBConnParams) Open(ctx context.Context, pingTimeout time.Duration) (*sql.DB, error) {
	db, err := sql.Open("postgres", p.DSN())
	if err != nil {
		return nil, fmt.Errorf("open target db: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping target db: %w", err)
	}
	return db, nil
}

// TableCopyResult reports how many rows were copied for one table.
type TableCopyResult struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

// copyBatchSize is the number of rows pushed per INSERT. server_stats can be
// large, so the copy streams the source cursor and flushes in bounded batches.
const copyBatchSize = 1000

// EnsureTargetSchema provisions the full DYLARIS schema on the target database.
// useTimescale decides whether server_stats becomes a hypertable on the target.
func EnsureTargetSchema(target *sql.DB, useTimescale bool) error {
	return database.EnsureSchema(target, useTimescale)
}

// CopyAllTables copies every public table from src into dst, parents before
// children (FK-topological order), after a single CASCADE TRUNCATE of all target
// tables so re-runs are idempotent. The source is only ever READ. progress, if
// non-nil, is called after each table with its name and the row count copied.
func CopyAllTables(ctx context.Context, src, dst *sql.DB, progress func(table string, rows int64)) ([]TableCopyResult, error) {
	ordered, err := discoverTablesOrdered(ctx, src)
	if err != nil {
		return nil, err
	}
	if len(ordered) == 0 {
		return nil, nil
	}

	if err := truncateAll(ctx, dst, ordered); err != nil {
		return nil, err
	}

	results := make([]TableCopyResult, 0, len(ordered))
	for _, table := range ordered {
		cols, err := tableColumns(ctx, src, table)
		if err != nil {
			return results, err
		}
		n, err := copyTableText(ctx, src, dst, table, cols)
		if err != nil {
			return results, err
		}
		results = append(results, TableCopyResult{Table: table, Rows: n})
		if progress != nil {
			progress(table, n)
		}
	}

	// Best-effort: realign sequences so post-switch inserts do not collide. Most
	// PKs are UUIDs now, so this is usually a no-op; failures are non-fatal.
	resetSequences(ctx, dst)

	return results, nil
}

// discoverTablesOrdered lists every public table and returns them parent-first.
func discoverTablesOrdered(ctx context.Context, db *sql.DB) ([]string, error) {
	tables, err := listPublicTables(ctx, db)
	if err != nil {
		return nil, err
	}
	deps, err := fkDependencies(ctx, db)
	if err != nil {
		return nil, err
	}
	return topoSortTables(tables, deps), nil
}

// listPublicTables returns all base tables in the public schema, sorted for
// determinism.
func listPublicTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename`)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// fkDependencies maps each child table to the parent tables it references via a
// foreign key. Self-references are dropped (they cannot affect ordering).
func fkDependencies(ctx context.Context, db *sql.DB) (map[string][]string, error) {
	const q = `
		SELECT tc.table_name AS child, ccu.table_name AS parent
		FROM information_schema.table_constraints tc
		JOIN information_schema.constraint_column_usage ccu
		  ON tc.constraint_name = ccu.constraint_name
		 AND tc.table_schema = ccu.table_schema
		WHERE tc.constraint_type = 'FOREIGN KEY'
		  AND tc.table_schema = 'public'`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read foreign keys: %w", err)
	}
	defer rows.Close()
	deps := map[string][]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, err
		}
		if child == parent {
			continue // self-reference, irrelevant to ordering
		}
		deps[child] = append(deps[child], parent)
	}
	return deps, rows.Err()
}

// topoSortTables orders tables so every parent precedes its children. deps maps
// child -> parents. Edges to tables outside the set are ignored. Genuine cycles
// (mutual FKs, which the DYLARIS schema does not have) are broken by appending
// the leftovers deterministically rather than failing.
func topoSortTables(tables []string, deps map[string][]string) []string {
	set := make(map[string]bool, len(tables))
	for _, t := range tables {
		set[t] = true
	}
	indeg := make(map[string]int, len(tables))
	children := make(map[string][]string) // parent -> children
	for _, t := range tables {
		indeg[t] = 0
	}
	for child, parents := range deps {
		if !set[child] {
			continue
		}
		for _, parent := range parents {
			if !set[parent] || parent == child {
				continue
			}
			indeg[child]++
			children[parent] = append(children[parent], child)
		}
	}

	// Kahn's algorithm; pop the alphabetically smallest ready node for stable output.
	var ready []string
	for _, t := range tables {
		if indeg[t] == 0 {
			ready = append(ready, t)
		}
	}
	sort.Strings(ready)

	out := make([]string, 0, len(tables))
	done := make(map[string]bool, len(tables))
	for len(ready) > 0 {
		t := ready[0]
		ready = ready[1:]
		if done[t] {
			continue
		}
		out = append(out, t)
		done[t] = true
		var newly []string
		for _, c := range children[t] {
			indeg[c]--
			if indeg[c] == 0 {
				newly = append(newly, c)
			}
		}
		if len(newly) > 0 {
			ready = append(ready, newly...)
			sort.Strings(ready)
		}
	}

	// Append anything left over (cycle) in deterministic order.
	if len(out) < len(tables) {
		leftover := make([]string, 0, len(tables)-len(out))
		for _, t := range tables {
			if !done[t] {
				leftover = append(leftover, t)
			}
		}
		sort.Strings(leftover)
		out = append(out, leftover...)
	}
	return out
}

// tableColumns returns the column names of a table in ordinal order.
func tableColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1
		 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, fmt.Errorf("columns of %s: %w", table, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// truncateAll clears every target table in one CASCADE TRUNCATE so the copy is
// idempotent regardless of FK order.
func truncateAll(ctx context.Context, dst *sql.DB, tables []string) error {
	quoted := make([]string, len(tables))
	for i, t := range tables {
		quoted[i] = quoteIdent(t)
	}
	stmt := "TRUNCATE TABLE " + strings.Join(quoted, ", ") + " CASCADE"
	if _, err := dst.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("truncate target: %w", err)
	}
	return nil
}

// copyTableText copies one table by casting every column to text on read and
// letting the target's input functions parse the text back. This is the most
// backend-agnostic generic copy: it round-trips uuid, jsonb, arrays, timestamps
// and bytea (hex) without per-type handling. NULLs are preserved.
func copyTableText(ctx context.Context, src, dst *sql.DB, table string, cols []string) (int64, error) {
	if len(cols) == 0 {
		return 0, nil
	}

	sel := make([]string, len(cols))
	for i, c := range cols {
		sel[i] = quoteIdent(c) + "::text"
	}
	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(sel, ", "), quoteIdent(table))
	rows, err := src.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", table, err)
	}
	defer rows.Close()

	insertPrefix := buildInsertPrefix(table, cols)

	holders := make([]sql.NullString, len(cols))
	scan := make([]interface{}, len(cols))
	for i := range holders {
		scan[i] = &holders[i]
	}

	batchArgs := make([]interface{}, 0, copyBatchSize*len(cols))
	rowsInBatch := 0
	var total int64

	flush := func() error {
		if rowsInBatch == 0 {
			return nil
		}
		stmt := insertPrefix + buildValuePlaceholders(rowsInBatch, len(cols))
		if _, err := dst.ExecContext(ctx, stmt, batchArgs...); err != nil {
			return fmt.Errorf("insert %s: %w", table, err)
		}
		total += int64(rowsInBatch)
		batchArgs = batchArgs[:0]
		rowsInBatch = 0
		return nil
	}

	for rows.Next() {
		if err := rows.Scan(scan...); err != nil {
			return total, fmt.Errorf("scan %s: %w", table, err)
		}
		for i := range holders {
			if holders[i].Valid {
				batchArgs = append(batchArgs, holders[i].String)
			} else {
				batchArgs = append(batchArgs, nil)
			}
		}
		rowsInBatch++
		if rowsInBatch >= copyBatchSize {
			if err := flush(); err != nil {
				return total, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return total, fmt.Errorf("read %s: %w", table, err)
	}
	if err := flush(); err != nil {
		return total, err
	}
	return total, nil
}

// buildInsertPrefix renders `INSERT INTO "t" ("c1", "c2") VALUES `.
func buildInsertPrefix(table string, cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quoteIdent(c)
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES ", quoteIdent(table), strings.Join(quoted, ", "))
}

// buildValuePlaceholders renders `($1, $2), ($3, $4)` for the given row/col counts.
func buildValuePlaceholders(rowCount, colCount int) string {
	var b strings.Builder
	n := 1
	for r := 0; r < rowCount; r++ {
		if r > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('(')
		for c := 0; c < colCount; c++ {
			if c > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "$%d", n)
			n++
		}
		b.WriteByte(')')
	}
	return b.String()
}

// resetSequences realigns owned sequences to MAX(col) so post-migration inserts
// do not collide. Entirely best-effort: any error is swallowed (UUID PKs make
// this irrelevant for most tables).
func resetSequences(ctx context.Context, dst *sql.DB) {
	const q = `
		SELECT s.relname AS seq, t.relname AS tbl, a.attname AS col
		FROM pg_class s
		JOIN pg_depend d ON d.objid = s.oid AND d.deptype = 'a'
		JOIN pg_class t ON t.oid = d.refobjid
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = d.refobjsubid
		WHERE s.relkind = 'S'`
	rows, err := dst.QueryContext(ctx, q)
	if err != nil {
		return
	}
	defer rows.Close()
	type seqRef struct{ seq, tbl, col string }
	var refs []seqRef
	for rows.Next() {
		var r seqRef
		if err := rows.Scan(&r.seq, &r.tbl, &r.col); err != nil {
			return
		}
		refs = append(refs, r)
	}
	for _, r := range refs {
		stmt := fmt.Sprintf(
			`SELECT setval('%s', COALESCE((SELECT MAX(%s) FROM %s), 1))`,
			r.seq, quoteIdent(r.col), quoteIdent(r.tbl))
		_, _ = dst.ExecContext(ctx, stmt)
	}
}

// quoteIdent double-quotes a Postgres identifier, doubling any embedded quotes.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
