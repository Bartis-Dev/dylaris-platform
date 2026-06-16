package services

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// TableVerify is the per-table comparison between source and target.
type TableVerify struct {
	Table        string `json:"table"`
	SourceExists bool   `json:"sourceExists"`
	TargetExists bool   `json:"targetExists"`
	SourceRows   int64  `json:"sourceRows"`
	TargetRows   int64  `json:"targetRows"`
	OK           bool   `json:"ok"`
}

// VerifyReport is the full source-vs-target comparison: every table present on
// both sides with matching row counts means OK.
type VerifyReport struct {
	OK        bool          `json:"ok"`
	Tables    []TableVerify `json:"tables"`
	Log       []string      `json:"log"`
	CheckedAt time.Time     `json:"checkedAt"`
}

// VerifyCopy compares the source and target databases: it checks that every
// public table exists on both sides and that COUNT(*) matches per table. Both
// connections are READ-ONLY here. The returned report carries a per-table
// breakdown plus a human-readable log. checkedAt must be stamped by the caller
// (the script/runtime forbids wall-clock reads inside pure logic), so this sets
// it from the passed clock value.
func VerifyCopy(ctx context.Context, src, dst *sql.DB, now time.Time) (VerifyReport, error) {
	srcTables, err := listPublicTables(ctx, src)
	if err != nil {
		return VerifyReport{}, fmt.Errorf("list source tables: %w", err)
	}
	dstTables, err := listPublicTables(ctx, dst)
	if err != nil {
		return VerifyReport{}, fmt.Errorf("list target tables: %w", err)
	}

	srcSet := toSet(srcTables)
	dstSet := toSet(dstTables)

	// Union of both sides, sorted, so a table missing on either side is reported.
	union := map[string]bool{}
	for t := range srcSet {
		union[t] = true
	}
	for t := range dstSet {
		union[t] = true
	}
	names := make([]string, 0, len(union))
	for t := range union {
		names = append(names, t)
	}
	sort.Strings(names)

	report := VerifyReport{OK: true, CheckedAt: now}
	for _, t := range names {
		tv := TableVerify{
			Table:        t,
			SourceExists: srcSet[t],
			TargetExists: dstSet[t],
		}
		if tv.SourceExists {
			if n, err := countRows(ctx, src, t); err == nil {
				tv.SourceRows = n
			} else {
				report.Log = append(report.Log, fmt.Sprintf("%s: failed to count source rows: %v", t, err))
			}
		}
		if tv.TargetExists {
			if n, err := countRows(ctx, dst, t); err == nil {
				tv.TargetRows = n
			} else {
				report.Log = append(report.Log, fmt.Sprintf("%s: failed to count target rows: %v", t, err))
			}
		}

		switch {
		case !tv.TargetExists:
			tv.OK = false
			report.Log = append(report.Log, fmt.Sprintf("%s: present on source, MISSING on target", t))
		case !tv.SourceExists:
			// Extra table on target only — not fatal (could be a leftover), but flag it.
			tv.OK = false
			report.Log = append(report.Log, fmt.Sprintf("%s: exists on target but not on source (extra table)", t))
		case tv.SourceRows != tv.TargetRows:
			tv.OK = false
			report.Log = append(report.Log, fmt.Sprintf("%s: ROW COUNT MISMATCH source=%d target=%d", t, tv.SourceRows, tv.TargetRows))
		default:
			tv.OK = true
			report.Log = append(report.Log, fmt.Sprintf("%s: %d rows match", t, tv.SourceRows))
		}

		if !tv.OK {
			report.OK = false
		}
		report.Tables = append(report.Tables, tv)
	}

	if report.OK {
		report.Log = append(report.Log, fmt.Sprintf("All %d tables present on both sides with matching row counts.", len(report.Tables)))
	} else {
		report.Log = append(report.Log, "Verification FAILED: see per-table entries above.")
	}
	return report, nil
}

// countRows runs COUNT(*) on a single table.
func countRows(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteIdent(table)).Scan(&n)
	return n, err
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
