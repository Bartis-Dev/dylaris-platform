package database

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestNoColumnIsBothNotNullAndSetNull catches a schema contradiction that only
// shows up when someone deletes a row:
//
//	user_id UUID NOT NULL REFERENCES users(id) ON DELETE SET NULL
//
// Deleting the referenced user fires the SET NULL, which then violates the
// NOT NULL. Postgres raises a not-null violation (23502), not a foreign-key
// one (23503), so error mapping written for FK violations misses it entirely
// and the caller gets a bare 500.
//
// ticket_messages.user_id shipped this way: anyone who had ever written a
// ticket message could never be deleted, and the admin UI's delete button just
// failed. A sweep of the live schema found it was the only instance; this test
// keeps it that way, at authoring time rather than at deletion time.
func TestNoColumnIsBothNotNullAndSetNull(t *testing.T) {
	// One column declaration per line is the convention in every CREATE TABLE
	// in this package, which is what makes a line-level check sound here.
	contradiction := regexp.MustCompile(`(?i)NOT\s+NULL[^,]*ON\s+DELETE\s+SET\s+NULL`)

	var findings []string
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.Contains(strings.TrimSpace(line), "//") {
				continue // prose, including this test's own explanation
			}
			if contradiction.MatchString(line) {
				findings = append(findings, path+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(findings) > 0 {
		t.Errorf("%d column(s) are NOT NULL with an ON DELETE SET NULL reference; "+
			"deleting the referenced row raises a not-null violation instead:\n  %s",
			len(findings), strings.Join(findings, "\n  "))
	}
}
