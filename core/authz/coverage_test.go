package authz

import (
	"net/http"
	"testing"

	"github.com/gorilla/mux"
)

func noop(w http.ResponseWriter, r *http.Request) {}

// buildSyntheticRouter stands in for the real router until phase 2 wires the
// harness against the actual route table. It has one exempt route, one
// "annotated" route, and one bare route with no capability.
func buildSyntheticRouter() *mux.Router {
	r := mux.NewRouter()
	r.HandleFunc("/healthz", noop).Methods("GET")                      // exempt
	r.HandleFunc("/api/servers/{id:[0-9]+}/files", noop).Methods("GET") // annotated (in required)
	r.HandleFunc("/api/servers/{id:[0-9]+}/bare", noop).Methods("GET")  // missing capability
	return r
}

func TestRouteCoverage_PermissiveModeIsAlwaysGreen(t *testing.T) {
	router := buildSyntheticRouter()
	violations, err := RouteCoverageViolations(router, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("permissive mode must report no violations, got %v", violations)
	}
}

func TestRouteCoverage_StrictModeFlagsBareRoute(t *testing.T) {
	router := buildSyntheticRouter()
	required := map[string]string{
		"/api/servers/{id:[0-9]+}/files": "files.read",
	}
	violations, err := RouteCoverageViolations(router, required, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 {
		t.Fatalf("strict mode should flag exactly the bare route, got %v", violations)
	}
	// The bare path must be named; the exempt + annotated routes must not appear.
	if got := violations[0]; !contains(got, "/api/servers/{id:[0-9]+}/bare") {
		t.Fatalf("violation = %q, want it to name the bare route", got)
	}
	for _, v := range violations {
		if contains(v, "/healthz") || contains(v, "/files") {
			t.Fatalf("exempt/annotated route wrongly flagged: %q", v)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
