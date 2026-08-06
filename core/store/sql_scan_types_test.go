package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoUUIDColumnIsScannedIntoAnInteger catches the second whole-statement
// failure this module cannot see without a real Postgres: a UUID column bound
// to an integer scan target.
//
//	SELECT id, url, requested_by FROM spark_profiles ...
//	var requestedBy sql.NullInt64
//	rows.Scan(&p.ID, &p.URL, &requestedBy)   // requested_by is UUID
//
// The scan fails for every row that has a value, and the surrounding loop
// dropped the row on error, so the endpoint answered 200 with an empty list.
// The spark profile list was shipped that way and never returned a single
// profile - every profile it stores carries the recording user's UUID.
//
// The pairing is only checked when the SELECT list and the Scan argument list
// have the same length, which is when position-to-position matching is sound.
func TestNoUUIDColumnIsScannedIntoAnInteger(t *testing.T) {
	root := ".." // the core module: store, database, handlers, services

	// Column names the schema declares UUID. "id" and "uuid" are deliberately
	// absent: "id" is SERIAL on most tables, and "uuid" is a VARCHAR server
	// identifier, so neither name alone proves the column's type.
	uuidColumns := map[string]bool{
		"actor_user_id": true, "added_by": true, "assigned_user_id": true,
		"changed_by": true, "created_by": true, "deleted_by": true,
		"installed_by": true, "invited_by": true, "owner_id": true,
		"owner_user_id": true, "requested_by": true, "target_user_id": true,
		"uploaded_by": true, "user_id": true,
	}

	sqlLiteral := regexp.MustCompile("(?s)`([^`]*)`")
	selectList := regexp.MustCompile(`(?is)^\s*SELECT\s+(.*?)\s+FROM\s`)
	scanCall := regexp.MustCompile(`(?s)\.Scan\(([^)]*)\)`)
	intTarget := regexp.MustCompile(`(?m)var\s+%s\s+(sql\.NullInt(?:64|32)|int\d*)\b`)

	var findings []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		src := string(raw)
		for _, m := range sqlLiteral.FindAllStringSubmatchIndex(src, -1) {
			query := src[m[2]:m[3]]
			cols := selectList.FindStringSubmatch(query)
			if cols == nil {
				continue
			}
			selected := splitTopLevel(cols[1])
			// The Scan that consumes this query is the next one after it, within
			// the same function.
			rest := src[m[3]:min(m[3]+3000, len(src))]
			sc := scanCall.FindStringSubmatchIndex(rest)
			if sc == nil {
				continue
			}
			targets := splitTopLevel(rest[sc[2]:sc[3]])
			if len(targets) != len(selected) {
				continue
			}
			// A name like "id" is declared int in one function and string in
			// another, so the declaration must be looked up inside the enclosing
			// function only - file-wide, every such name reads as both.
			fnStart := strings.LastIndex(src[:m[2]], "\nfunc ")
			if fnStart < 0 {
				fnStart = 0
			}
			scope := src[fnStart : m[3]+sc[1]]
			for i, col := range selected {
				if !uuidColumns[trailingIdent(col)] {
					continue
				}
				name := strings.TrimPrefix(strings.TrimSpace(targets[i]), "&")
				decl := regexp.MustCompile(strings.Replace(intTarget.String(), "%s", regexp.QuoteMeta(name), 1))
				if decl.MatchString(scope) {
					findings = append(findings, path+": "+trailingIdent(col)+" (UUID) scanned into "+name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(findings) > 0 {
		t.Errorf("%d UUID column(s) bound to an integer scan target; every row with a value fails to scan:\n  %s",
			len(findings), strings.Join(findings, "\n  "))
	}
}

// splitTopLevel splits a comma-separated SQL or Go argument list, ignoring
// commas nested in parentheses (COALESCE(x,0), fn(a,b)).
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if tail := strings.TrimSpace(s[start:]); tail != "" {
		out = append(out, tail)
	}
	return out
}

// trailingIdent reduces a SELECT item to the column name it ultimately reads:
// "t.requested_by" -> "requested_by", "COALESCE(target_port,0)" -> "target_port".
func trailingIdent(item string) string {
	item = strings.TrimSpace(item)
	if i := strings.LastIndex(item, "("); i >= 0 {
		item = item[i+1:]
		item = strings.TrimSuffix(item, ")")
		if j := strings.Index(item, ","); j >= 0 {
			item = item[:j]
		}
	}
	item = strings.TrimSpace(item)
	if i := strings.LastIndex(item, "."); i >= 0 {
		item = item[i+1:]
	}
	if i := strings.Index(item, " "); i >= 0 { // "col AS alias"
		item = item[:i]
	}
	return strings.TrimSpace(item)
}
