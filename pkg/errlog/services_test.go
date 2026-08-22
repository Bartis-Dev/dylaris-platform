package errlog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// errlogNewCall matches the service-name literal in an errlog.New(...) call, so
// the producers can be checked against the list the reader scans.
var errlogNewCall = regexp.MustCompile(`errlog\.New\([^,]+,\s*"([^"]+)"`)

// A service name that no reader scans for turns its logger into a hole: the
// entries are written, the ACL permits them, and the panel section for that
// component stays empty - which reads as "no errors", not as "not wired".
//
// That is not hypothetical. The edge was renamed gate -> edge and its producer
// moved with it; the reader's list did not. Every edge error went to
// dylaris:errors:edge:* while Core scanned dylaris:errors:gate:*, for as long as
// nobody thought to compare the two strings.
//
// So this walks the repo and holds every errlog.New call against Services.
func TestEveryProducerUsesAKnownServiceName(t *testing.T) {
	root := repoRoot(t)
	offenders := map[string][]string{}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable tree entry is not this test's business
		}
		if info.IsDir() {
			switch info.Name() {
			case "node_modules", ".git", "third_party", "backup", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, m := range errlogNewCall.FindAllStringSubmatch(string(body), -1) {
			if !IsKnownService(m[1]) {
				rel, _ := filepath.Rel(root, path)
				offenders[m[1]] = append(offenders[m[1]], filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for svc, files := range offenders {
		t.Errorf("errlog.New(%q) in %v: not in Services %v, so nothing reads those entries", svc, files, Services)
	}
}

// repoRoot walks up from this package to the directory holding go.work, which is
// the repo root for both modules that carry a copy of this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, serr := os.Stat(filepath.Join(dir, "go.work")); serr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("no go.work above this package; the producer scan needs the repo root")
	return ""
}
