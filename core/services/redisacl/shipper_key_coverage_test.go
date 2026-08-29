package redisacl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// shipperSourceDir is the log-shipper's source, a sibling Go module in this
// repo. Read rather than imported, for the same reason as nodeSourceDir: it is
// package main in its own module.
const shipperSourceDir = "../../../log-shipper"

// TestShipperACLGrantsEveryKeyItUses is the twin of the node sweep, for the one
// module that had no sweep at all.
//
// It matters more here than anywhere else in this repo. The shipper's credential
// is passed into the MC container's ENVIRONMENT, and that container runs the
// tenant's own plugins - so this rule set is what tenant-authored code may do to
// Redis. It was a namespace wildcard, which included the keys that enforce
// limits against that tenant: dylaris:server:<u>:disk_full is the whole
// disk-quota guard, set BEFORE the graceful stop (deliberately - the reconciler
// ticks during it) and therefore deletable by code still running in the
// container being stopped, with desired_state left at "online" so the reconciler
// starts it straight back up.
//
// The grant is now an enumeration, and an enumeration rots: the next key the
// shipper starts using answers NOPERM and whatever it is for silently does
// nothing. That is what this test is for. If it fails, decide deliberately -
// add the key to BuildShipperACLRules, do NOT restore the wildcard.
func TestShipperACLGrantsEveryKeyItUses(t *testing.T) {
	keys := shipperKeyLiterals(t)

	// An extraction that stops matching would turn this into a test that passes
	// by looking at nothing.
	if len(keys) < 6 {
		t.Fatalf("only %d key literals found in %s; the extraction is broken, not the shipper", len(keys), shipperSourceDir)
	}

	grants := shipperGrantedPatterns(t)
	for key, file := range keys {
		if !coveredBy(grants, key) {
			t.Errorf("log-shipper/%s uses Redis key %q, which BuildShipperACLRules does not grant; "+
				"every command on it answers NOPERM and whatever it is for silently does nothing", file, key)
		}
	}
}

// shipperKeyLiterals returns every Redis key literal in the shipper's non-test
// sources, with the server uuid generalised to "*" so it matches the patterns.
//
// The shipper builds every key with fmt.Sprintf("dylaris:server:%s:...", uuid),
// so the literal carries a %s where the uuid goes.
func shipperKeyLiterals(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(shipperSourceDir)
	if err != nil {
		t.Fatalf("read log-shipper source: %v (if log-shipper/ moved, this test moves with it - do not delete it)", err)
	}

	fset := token.NewFileSet()
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(shipperSourceDir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse log-shipper/%s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil || !keyLiteralRE.MatchString(v) {
				return true
			}
			// The uuid placeholder becomes the uuid this test grants for. A
			// trailing "%s" (the sub-server in ...:logs:%s) becomes "*", which
			// is what the pattern for it says.
			k := strings.Replace(v, "dylaris:server:%s:", "dylaris:server:uuid-a:", 1)
			k = strings.ReplaceAll(k, "%s", "*")
			out[k] = name
			return true
		})
	}
	return out
}

// shipperGrantedPatterns returns the key patterns BuildShipperACLRules grants.
func shipperGrantedPatterns(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, r := range BuildShipperACLRules("pw", "uuid-a") {
		s, ok := r.(string)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(s, "%R~"), strings.HasPrefix(s, "%W~"), strings.HasPrefix(s, "%RW~"):
			out = append(out, s[strings.Index(s, "~")+1:])
		case strings.HasPrefix(s, "~"):
			out = append(out, strings.TrimPrefix(s, "~"))
		case strings.HasPrefix(s, "&"):
			out = append(out, strings.TrimPrefix(s, "&"))
		}
	}
	if len(out) == 0 {
		t.Fatal("BuildShipperACLRules produced no key patterns")
	}
	return out
}
