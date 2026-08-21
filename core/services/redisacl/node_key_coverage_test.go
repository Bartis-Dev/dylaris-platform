package redisacl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// nodeSourceDir is the node agent's source, a sibling Go module in this repo.
// Read rather than imported: node is package main in its own module, so the
// only way to compare its keys against these rules is to look at the source.
const nodeSourceDir = "../../../node"

// keyLiteralRE matches a Redis key literal: one of the namespaces the platform
// uses, followed by a run of non-space characters.
//
// The no-whitespace rule is what separates keys from prose - the node logs
// several messages that begin "link: ..." and none of them is a key. Comments
// are excluded structurally rather than by pattern (see nodeKeyLiterals): the
// first version of this test scanned raw bytes and reported the sftp:fail key
// named in the comment that documents why it is gone.
var keyLiteralRE = regexp.MustCompile(`^(?:dylaris|beam|sftp|node|link|online_link|hub|edge|sys):\S*$`)

// TestNodeACLGrantsEveryKeyTheNodeUses is the check that a behavioural test
// cannot be: it compares what the node WRITES against what Core GRANTS it.
//
// Mandatory Redis ACL made these two halves a contract, and nothing enforced it.
// A key outside every granted pattern does not fail loudly - Redis answers
// NOPERM, go-redis returns it as an ordinary error, and node code that discards
// the error (most of it does, these are best-effort writes) carries on as if the
// write happened. The feature behind that key is then simply absent, with
// nothing in any log to say so.
//
// That is what happened to the SFTP brute-force lockout. Its counter lived at
// "sftp:fail:<username>" while the grant covered sftp:auth:* and
// sftp:node:<token>:*, so the counter could never be incremented and the read
// answered 0 forever: ten failed passwords in a row locked nobody out, on every
// node in the fleet, for as long as the ACL had been mandatory.
//
// The test builds the real rules with "*" standing in for the node token and the
// server uuid. It therefore proves a namespace is granted at all, not that it is
// granted to the right tenant - scoping is what the other tests in this package
// cover.
func TestNodeACLGrantsEveryKeyTheNodeUses(t *testing.T) {
	grants := grantedPatterns(t)
	keys := nodeKeyLiterals(t)

	// An extraction that stops matching would turn this into a test that passes
	// by looking at nothing, which is the failure mode it is least able to
	// notice on its own.
	if len(keys) < 20 {
		t.Fatalf("only %d key literals found in %s; the extraction is broken, not the node", len(keys), nodeSourceDir)
	}

	for key, file := range keys {
		if !coveredBy(grants, key) {
			t.Errorf("node/%s uses Redis key %q, which no pattern in BuildNodeACLRules grants; "+
				"every command on it answers NOPERM and whatever it is for silently does nothing", file, key)
		}
	}
}

// nodeKeyLiterals returns every Redis key literal in the node's non-test
// sources, mapped to the file it came from. Parsed rather than grepped so that
// comments and identifiers cannot contribute.
func nodeKeyLiterals(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(nodeSourceDir)
	if err != nil {
		t.Fatalf("read node source: %v (if node/ moved, this test moves with it - do not delete it)", err)
	}

	fset := token.NewFileSet()
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(nodeSourceDir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse node/%s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err == nil && keyLiteralRE.MatchString(v) {
				out[v] = name
			}
			return true
		})
	}
	return out
}

// grantedPatterns returns the key patterns of the node ACL user, with the node
// token and the server uuid generalised to "*".
func grantedPatterns(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, r := range BuildNodeACLRules("*", "pw", []string{"*"}) {
		s, ok := r.(string)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(s, "%R~"):
			out = append(out, strings.TrimPrefix(s, "%R~"))
		case strings.HasPrefix(s, "~"):
			out = append(out, strings.TrimPrefix(s, "~"))
		case strings.HasPrefix(s, "&"):
			// Pub/sub channels. The node PUBLISHes to these, so they belong in the
			// same sweep even though they are not keys.
			out = append(out, strings.TrimPrefix(s, "&"))
		}
	}
	if len(out) == 0 {
		t.Fatal("BuildNodeACLRules produced no key patterns")
	}
	return out
}

// coveredBy reports whether key falls under any granted pattern.
//
// A pattern with a wildcard covers everything under its literal prefix, which is
// also how a bare prefix in the source ("dylaris:server:", completed at runtime)
// gets matched. A pattern without one grants exactly itself.
func coveredBy(patterns []string, key string) bool {
	for _, p := range patterns {
		if prefix, _, hasWildcard := strings.Cut(p, "*"); hasWildcard {
			if strings.HasPrefix(key, prefix) {
				return true
			}
			continue
		}
		if key == p {
			return true
		}
	}
	return false
}
