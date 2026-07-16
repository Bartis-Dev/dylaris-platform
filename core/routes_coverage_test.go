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
