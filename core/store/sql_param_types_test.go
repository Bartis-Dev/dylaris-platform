package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoParameterIsBothAssignedAndComparedToALiteral catches a whole-statement
// failure that no other test here can see, because nothing in this repo runs SQL
// against a real Postgres - the store tests use fakes, so a query that Postgres
// refuses to even prepare looks perfectly healthy in CI.
//
// The shape: a placeholder used twice, once assigned to a varchar column and
// once compared to an untyped string literal.
//
//	UPDATE users SET role = $1, is_admin = ($1 = 'admin') WHERE id = $2
//
// The driver declares no type, so Postgres deduces one from each use and
// refuses the contradiction:
//
//	ERROR: inconsistent types deduced for parameter $1
//	DETAIL: text versus character varying
//
// The statement never executes - not for some rows, not sometimes. Two shipped
// queries had it: setting a user's role, and moving a ticket to resolved or
// closed. Both are single-line features that simply never worked, and both
// surfaced as a generic 500.
//
// Reusing a placeholder is fine in general (ON CONFLICT DO UPDATE and UNION
// branches do it all over this package). What is not fine is reusing one whose
// other use is a bare quoted literal, which is what this test looks for.
func TestNoParameterIsBothAssignedAndComparedToALiteral(t *testing.T) {
	root := ".." // the core module: store, database, handlers, services
	sqlLiteral := regexp.MustCompile("(?s)`([^`]*)`")
	isSQL := regexp.MustCompile(`(?i)\b(SELECT|INSERT|UPDATE|DELETE)\b`)
	// $N compared directly to a single-quoted literal, either order.
	paramVsLiteral := regexp.MustCompile(`\$(\d+)\s*(?:=|<>|!=)\s*'|'[^']*'\s*(?:=|<>|!=)\s*\$(\d+)`)
	anyParam := regexp.MustCompile(`\$(\d+)`)

	var findings []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, m := range sqlLiteral.FindAllStringSubmatch(string(src), -1) {
			q := m[1]
			if !isSQL.MatchString(q) || !anyParam.MatchString(q) {
				continue
			}
			for _, lit := range paramVsLiteral.FindAllStringSubmatch(q, -1) {
				n := lit[1]
				if n == "" {
					n = lit[2]
				}
				// Conflict only when that same placeholder is used somewhere else
				// too; compared to a literal and nothing else is unambiguous.
				if len(anyParam.FindAllString(q, -1)) > 0 &&
					strings.Count(q, "$"+n) > 1 {
					findings = append(findings, path+": $"+n+" in "+strings.Join(strings.Fields(q), " "))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(findings) > 0 {
		t.Errorf("%d quer(ies) reuse a placeholder that is also compared to a string literal; "+
			"Postgres will refuse to prepare them:\n  %s", len(findings), strings.Join(findings, "\n  "))
	}
}
