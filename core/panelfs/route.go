package panelfs

import (
	"io/fs"
	"path"
	"strings"
)

// ExportParam is the placeholder a dynamic route segment is exported under.
//
// CROSS-LANGUAGE CONTRACT with panel/src/lib/exportParam.ts. The panel's
// generateStaticParams returns this literal, so the build writes
// dist/servers/__param__/audit.html, and this file reads the directory names
// back to learn where the wildcards are.
//
// Derived, not declared: a new dynamic route in the panel needs no change here.
// A hand-kept table would be a second place to update, and the one that gets
// forgotten is always the one that turns a working page into a 404.
const ExportParam = "__param__"

// routeTree matches a request path against the exported files.
//
// A trie rather than a scan, for one behaviour a scan makes easy to get wrong:
// an exact segment must beat a wildcard at the same position. /servers/audit
// would otherwise be answerable by servers/__param__.html, and the more specific
// file is always the right answer.
type routeTree struct {
	root  *routeNode
	count int
}

type routeNode struct {
	children map[string]*routeNode
	wildcard *routeNode
	// file is the export that answers this exact path, or "" when the node is
	// only a step on the way to a deeper one.
	file string
}

func newNode() *routeNode { return &routeNode{children: map[string]*routeNode{}} }

// buildRouteTree walks the export and indexes every .html file by the request
// path it answers. dist/login.html answers /login; dist/index.html answers /.
func buildRouteTree(fsys fs.FS) (*routeTree, error) {
	t := &routeTree{root: newNode()}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		route := strings.TrimSuffix(p, ".html")
		// index.html is the root, and a nested index.html would be its
		// directory - Next writes flat files today, but the rule costs nothing
		// and stops a future output shape from serving "/x/index".
		if route == "index" {
			route = ""
		} else {
			route = strings.TrimSuffix(route, "/index")
		}
		t.insert(route, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (t *routeTree) insert(route, file string) {
	n := t.root
	if route != "" {
		for _, seg := range strings.Split(route, "/") {
			if seg == ExportParam {
				if n.wildcard == nil {
					n.wildcard = newNode()
				}
				n = n.wildcard
				continue
			}
			child, ok := n.children[seg]
			if !ok {
				child = newNode()
				n.children[seg] = child
			}
			n = child
		}
	}
	if n.file == "" {
		t.count++
	}
	n.file = file
}

// lookup resolves a request path to an exported file.
//
// The walk backtracks: a path can descend through an exact segment, find
// nothing at the end, and still be answerable through the wildcard branch at
// that position. Without backtracking a route like /servers/new (a real page)
// sitting beside /servers/[id] would make /servers/123 unreachable the moment
// the first segment matched exactly.
func (t *routeTree) lookup(reqPath string) (string, bool) {
	clean := strings.Trim(path.Clean("/"+reqPath), "/")
	var segs []string
	if clean != "" && clean != "." {
		segs = strings.Split(clean, "/")
	}
	return walk(t.root, segs)
}

func walk(n *routeNode, segs []string) (string, bool) {
	if n == nil {
		return "", false
	}
	if len(segs) == 0 {
		if n.file == "" {
			return "", false
		}
		return n.file, true
	}
	if child, ok := n.children[segs[0]]; ok {
		if file, found := walk(child, segs[1:]); found {
			return file, true
		}
	}
	return walk(n.wildcard, segs[1:])
}
