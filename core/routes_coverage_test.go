package main

import (
	"testing"

	"dylaris-core/authz"
	"dylaris-core/handlers"
	"dylaris-core/services"

	"github.com/gorilla/mux"
)

// stubRouter builds the real router with a nil-ish AppState. Handler constructors
// only store the pointer, so this is safe; the router is walked, never served.
func stubRouter(t *testing.T) *mux.Router {
	t.Helper()
	appState := &handlers.AppState{
		FeatureFlags: services.NewFeatureFlags(nil),
		Authz:        authz.NewResolver(nil),
	}
	authHandler := handlers.NewAuthHandler(appState, "test-secret")
	root, _ := buildAPIRouter(appState, authHandler, routeCfg{JWTSecret: "test-secret"})
	return root
}

func TestRouteCoverage_PermissiveRealRouterIsGreen(t *testing.T) {
	violations, err := authz.RouteCoverageViolations(stubRouter(t), requiredCaps, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("permissive mode must be green, got %v", violations)
	}
}

func TestRequiredCapsIntegrity(t *testing.T) {
	tmpls := map[string]bool{}
	_ = stubRouter(t).Walk(func(rt *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		if tpl, err := rt.GetPathTemplate(); err == nil {
			tmpls[tpl] = true
		}
		return nil
	})
	for tpl, capID := range requiredCaps {
		if !authz.Has(capID) {
			t.Errorf("requiredCaps[%q]=%q is not a catalog capability", tpl, capID)
		}
		if !tmpls[tpl] {
			t.Errorf("requiredCaps key %q matches no registered route template", tpl)
		}
	}
}

// TestEveryRouteIsClassified is the Phase 4 Task 22 pre-flight for the strict
// coverage flip (Task 23): every registered route template must land in
// EXACTLY ONE of requiredCaps, authz.ExemptRoutes, or authz.InHandlerAuthzRoutes.
// A route that is none of these is a gap that the strict flip would leave
// ungated, so this fails loudly and prints every unclassified template.
func TestEveryRouteIsClassified(t *testing.T) {
	var unclassified []string
	_ = stubRouter(t).Walk(func(rt *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		tpl, err := rt.GetPathTemplate()
		if err != nil {
			return nil
		}
		if authz.ExemptRoutes[tpl] || authz.InHandlerAuthzRoutes[tpl] {
			return nil
		}
		if _, ok := requiredCaps[tpl]; ok {
			return nil
		}
		unclassified = append(unclassified, tpl)
		return nil
	})
	if len(unclassified) != 0 {
		t.Fatalf("unclassified routes (add to requiredCaps/ExemptRoutes/InHandlerAuthzRoutes): %v", unclassified)
	}
}

// TestBucketsAreDisjoint guards the "EXACTLY one bucket" half of the coverage
// invariant: no route template may appear in more than one of requiredCaps /
// ExemptRoutes / InHandlerAuthzRoutes. Without this, a template landing in both
// requiredCaps and ExemptRoutes would be silently treated as exempt (ExemptRoutes
// short-circuits first in RouteCoverageViolations), quietly un-gating a route that
// is supposed to carry a capability. Together with TestEveryRouteIsClassified
// (at least one bucket) this pins exactly-one.
func TestBucketsAreDisjoint(t *testing.T) {
	for tpl := range requiredCaps {
		if authz.ExemptRoutes[tpl] {
			t.Errorf("template %q is in BOTH requiredCaps and ExemptRoutes", tpl)
		}
		if authz.InHandlerAuthzRoutes[tpl] {
			t.Errorf("template %q is in BOTH requiredCaps and InHandlerAuthzRoutes", tpl)
		}
	}
	for tpl := range authz.ExemptRoutes {
		if authz.InHandlerAuthzRoutes[tpl] {
			t.Errorf("template %q is in BOTH ExemptRoutes and InHandlerAuthzRoutes", tpl)
		}
	}
}

// TestRouteCoverage_StrictRealRouter is the standing "nothing is un-gateable"
// guarantee (Phase 4 Task 23): every non-exempt, non-in-handler route must declare
// a capability in requiredCaps, or the build fails. This is the enforcement
// cut-over's completion - from here, adding a new /api route without a cap (or an
// explicit exempt/in-handler classification) breaks the test suite.
func TestRouteCoverage_StrictRealRouter(t *testing.T) {
	violations, err := authz.RouteCoverageViolations(stubRouter(t), requiredCaps, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("STRICT coverage: %d un-gated route(s): %v", len(violations), violations)
	}
}

// TestPrintRouteTemplates: `go test -run TestPrintRouteTemplates -v` prints every
// template + methods so later batches copy exact requiredCaps keys.
func TestPrintRouteTemplates(t *testing.T) {
	if testing.Short() {
		t.Skip("printer utility")
	}
	_ = stubRouter(t).Walk(func(rt *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		tpl, _ := rt.GetPathTemplate()
		methods, _ := rt.GetMethods()
		t.Logf("%v %s", methods, tpl)
		return nil
	})
}
