package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/services"

	"github.com/gorilla/mux"
)

// The Solder mirror is unauthenticated by protocol - a Technic launcher fetches
// these URLs with no credential - so its key prefix list IS the access
// boundary. Everything reachable through it is public to anyone holding the
// URL, which makes "which prefixes" a security decision, not a routing detail.
//
// MEASURED on the testbed before the fix: a pack marked private answered 404 on
// /solder/api/modpack/{slug} exactly as designed, while the very same pack's
// build manifest came back in full from
// /solder/mirror/solder/manifests/{owner}/{slug}/{version}/build.json - the
// complete mod list of a pack whose launcher API refuses to admit it exists.
// The cause was a prefix list wide enough (solder/) to cover an artifact no
// launcher ever requests.
//
// The guard runs before any storage is built, so the two outcomes separate
// cleanly: a rejected key never gets that far and answers 404, while an allowed
// key falls through to the unconfigured provider. The assertion is therefore
// "404 or not", never the downstream status.
func newSolderMirrorTestRouter(t *testing.T) *mux.Router {
	t.Helper()
	fs := newCoreStorageHTTPFakeStore()
	// The mirror's first gate. Default is off, and an off flag would make every
	// case below 403 for the wrong reason.
	fs.kv["feature_modpacks_enabled"] = "true"

	st := &AppState{Store: fs}
	st.FeatureFlags = services.NewFeatureFlags(st.Store)
	h := NewSolderHandler(st)

	r := mux.NewRouter()
	r.HandleFunc("/solder/mirror/{rest:.*}", h.SolderMirror).Methods("GET")
	return r
}

func TestSolderMirror_ServesOnlyLauncherFetchedPrefixes(t *testing.T) {
	const owner = "b083ff0c-fc16-47c5-9700-7b16486583e7"

	cases := []struct {
		name    string
		key     string
		allowed bool
	}{
		{
			name:    "a build manifest is internal and must not be served",
			key:     "solder/manifests/" + owner + "/handtest-pack/2.0.0/build.json",
			allowed: false,
		},
		{
			name:    "nor may anything else that lands under solder/",
			key:     "solder/somethingnew/" + owner + "/secret.json",
			allowed: false,
		},
		{
			name:    "uploaded pack content lives under packs/ and was never servable",
			key:     "packs/" + owner + "/mods/private-mod/private-mod-1.0.zip",
			allowed: false,
		},
		{
			name:    "a mod zip is what the launcher downloads",
			key:     "solder/mods/" + owner + "/p7dr8msh/p7dr8msh-0.92.11+1.20.1.zip",
			allowed: true,
		},
		{
			name:    "so is a loader zip, referenced from the manifest as a mods entry",
			key:     "loaders/fabric/1.20.1/0.15.11/loader.zip",
			allowed: true,
		},
		{
			name:    "and a rendered mrpack, which a node fetches when installing a pack",
			key:     "modpacks/" + owner + "/handtest-pack/2.0.0/pack.mrpack",
			allowed: true,
		},
		{
			name:    "traversal stays rejected",
			key:     "solder/mods/../../etc/passwd",
			allowed: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/solder/mirror/"+c.key, nil)
			newSolderMirrorTestRouter(t).ServeHTTP(rec, req)

			// 301 is mux, not the guard: it path-cleans the URL and redirects
			// before the handler ever runs, so a traversal never reaches the
			// prefix check. Counting it as rejected keeps that case honest
			// rather than dropping it for not fitting the discriminator.
			rejected := rec.Code == http.StatusNotFound || rec.Code == http.StatusMovedPermanently
			if c.allowed && rejected {
				t.Errorf("key %q was rejected by the prefix guard; a launcher fetches it and the install breaks", c.key)
			}
			if !c.allowed && !rejected {
				t.Errorf("key %q got past the prefix guard (status %d); this route is unauthenticated, so that key is public", c.key, rec.Code)
			}
		})
	}
}
