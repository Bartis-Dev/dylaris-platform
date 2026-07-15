package authz

import (
	"fmt"

	"github.com/gorilla/mux"
)

// ExemptRoutes is the allowlist of routes that legitimately carry NO capability
// because they are auth-exempt or public: health checks, login, the setup
// wizard, tokenized share/tab-proxy links, and external webhooks. Keyed by the
// mux path template (route.GetPathTemplate()). Phase 2 fills the companion
// route->capability map and flips strict mode on; anything not exempt and not
// mapped then fails the build, guaranteeing "nothing is un-gateable".
//
// This is the phase-1 seed; phase 2 reconciles it against the real router.
var ExemptRoutes = map[string]bool{
	"/healthz":                        true,
	"/api/auth/login":                 true,
	"/api/status":                     true,
	"/api/setup/status":               true,
	"/api/setup/admin":                true,
	"/api/auth/demo-login":            true,
	"/api/system/capabilities":        true,
	"/api/system/core-info":           true,
	"/api/share/{token}":              true,
	"/api/tabproxy/{token}":           true,
	"/api/tabproxy/{token}/{rest:.*}": true,
	"/api/external/rcon/{uuid}/exec":  true, // API-key auth path, not session authz
}

// RouteCoverageViolations walks router and returns a human-readable list of
// routes that lack a declared capability. required maps a route path template
// to its capability id; ExemptRoutes are always allowed to be uncovered.
//
// Phase 1 runs in PERMISSIVE mode (strict=false): it returns nil so the test is
// green before any route is annotated. Phase 2 populates required for every
// route and calls this with strict=true from a test that walks the REAL router
// (extracted into a testable builder), asserting zero violations - the standing
// guarantee that every route is gated by a meaningful capability.
func RouteCoverageViolations(router *mux.Router, required map[string]string, strict bool) ([]string, error) {
	if !strict {
		return nil, nil
	}
	var violations []string
	err := router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		tmpl, terr := route.GetPathTemplate()
		if terr != nil {
			// Routes without a path template (pure matchers / middleware mounts)
			// carry nothing to gate; skip them.
			return nil
		}
		if ExemptRoutes[tmpl] {
			return nil
		}
		if _, ok := required[tmpl]; !ok {
			methods, _ := route.GetMethods()
			violations = append(violations, fmt.Sprintf("%v %s", methods, tmpl))
		}
		return nil
	})
	return violations, err
}
