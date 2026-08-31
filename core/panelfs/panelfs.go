// Package panelfs serves the panel's static export out of Core's own binary.
//
// The panel used to be a second image running a Next server. It has no
// server-side surface left - no API routes, no server actions, every page a
// client component - so the only thing that server still did was hand out HTML
// shells and hashed chunks, and mint a CSP nonce per request. The first two are
// files; the third moved here.
//
// What that buys, in order of how much it matters:
//
//   - Same origin by construction. The panel and /api can no longer be on
//     different origins by accident, which is what makes an HttpOnly session
//     cookie possible at all.
//   - One version. The panel and Core were stamped separately and could drift;
//     an image cannot drift from itself.
//   - No Node in the runtime image, and no second service to route to.
package panelfs

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// dist is the panel's `next build` output, copied in before compilation. The
// repo carries a one-page placeholder so the package always compiles; CI
// overwrites the directory with the real export.
//
// all: rather than a bare pattern, because the export contains files Go would
// otherwise skip - anything under a directory whose name starts with _ , which
// is every hashed chunk under _next.
//
//go:embed all:dist
var dist embed.FS

// Handler serves the panel. It is the router's fallback, so it only sees
// requests no API route claimed.
type Handler struct {
	files fs.FS
	// routes maps a request path onto the exported file that answers it,
	// including the wildcard segments dynamic routes are exported under.
	routes *routeTree
	// configJS is the runtime API-URL assignment, rendered once at construction
	// and injected INTO each page rather than served as its own file. A shell
	// entrypoint used to write it into the panel image on container start; Core
	// holds the value already.
	configJS []byte
	// csp is everything about the policy except the nonce, which is per
	// response.
	csp cspConfig
	// Built reports whether a real export was embedded. False means the
	// placeholder, which the caller logs loudly at boot - a Core serving the
	// placeholder answers every panel URL with a page nobody wants.
	Built bool
}

// New prepares the handler. apiURL is PANEL_API_URL: empty for the normal
// same-origin deployment, set only by an operator who still serves the API on a
// separate hostname. frontendURL and tabSuffix are Core's own settings, which is
// the point - the panel container used to need its own copy of tabSuffix purely
// to write the CSP, and nothing compared the two.
func New(apiURL, tabSuffix string) (*Handler, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	tree, err := buildRouteTree(sub)
	if err != nil {
		return nil, err
	}
	// A real export always carries the hashed chunk directory. The placeholder
	// is a single HTML file, so its absence is the honest test for "was the
	// panel actually built into this binary".
	_, statErr := fs.Stat(sub, "_next")
	return &Handler{
		files:    sub,
		routes:   tree,
		configJS: renderConfigJS(apiURL),
		csp:      cspConfig{apiOrigin: originOf(apiURL), tabSuffix: normalizeTabSuffix(tabSuffix)},
		Built:    statErr == nil,
	}, nil
}

// Routes is how many request patterns were found. Logged at boot; a sudden drop
// is the signature of an export that half-failed.
func (h *Handler) Routes() int { return h.routes.count }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")

	// A real file wins over a route: hashed chunks, fonts, icons. Directories
	// are not served - a request for one falls through to the route lookup,
	// where /servers means servers.html rather than a directory listing.
	if f, ok := h.openFile(p); ok {
		defer f.Close()
		h.serveAsset(w, r, p, f)
		return
	}

	file, found := h.routes.lookup(p)
	if !found {
		file = "404.html"
	}
	h.serveHTML(w, r, file, found)
}
