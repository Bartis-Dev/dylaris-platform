package main

import (
	"os"
	"strings"
	"testing"

	"dylaris-core/apidoc"

	"github.com/gorilla/mux"
)

type walkedRoute struct {
	methods []string
	path    string
}

func (w walkedRoute) keys() []string {
	if len(w.methods) == 0 {
		return []string{"(any) " + w.path}
	}
	out := make([]string, 0, len(w.methods))
	for _, m := range w.methods {
		out = append(out, m+" "+w.path)
	}
	return out
}

// TestAPIDocCoversEveryRegisteredRoute is the guard that matters: the generator
// reads routes.go as text, so a registration written in a shape it does not
// recognise would simply vanish from the reference with nothing failing. This
// asks the built router what it actually serves and demands the two agree.
//
// The only permitted difference is a subrouter's own mount point - mux reports
// "/api" and "/solder" as routes because a subrouter is attached as one, but
// neither serves anything itself, and it is the nil handler that says so. A
// path test would not do: "/api/tabproxy/{token}" is a real route that also
// carries no methods and also has a longer sibling below it.
func TestAPIDocCoversEveryRegisteredRoute(t *testing.T) {
	var walked []walkedRoute
	if err := stubRouter(t).Walk(func(rt *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		tpl, err := rt.GetPathTemplate()
		if err != nil || rt.GetHandler() == nil {
			return nil
		}
		methods, _ := rt.GetMethods()
		walked = append(walked, walkedRoute{methods: methods, path: tpl})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	routes, err := apidoc.Parse("routes.go", []string{"handlers"})
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, r := range routes {
		for _, k := range (walkedRoute{methods: r.Methods, path: r.Path}).keys() {
			documented[k] = true
		}
	}

	served := map[string]bool{}
	for _, w := range walked {
		for _, k := range w.keys() {
			served[k] = true
			if !documented[k] {
				t.Errorf("route %q is served but missing from the reference: the generator did not recognise its registration", k)
			}
		}
	}

	for k := range documented {
		if !served[k] {
			t.Errorf("route %q is in the reference but the router does not serve it", k)
		}
	}
}

// TestAPIDocIsCurrent keeps the checked-in document honest. It fails on any
// change to a route, a capability or a handler doc comment that was not
// followed by a regeneration.
func TestAPIDocIsCurrent(t *testing.T) {
	want, err := apidoc.Generate(".")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(apidoc.DocPath)
	if err != nil {
		t.Fatalf("%v (run: go run ./cmd/apidocs)", err)
	}
	got := apidoc.Normalize(string(raw))
	if got == want {
		return
	}

	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		g, w := "", ""
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("%s is out of date, run: go run ./cmd/apidocs\n"+
				"first difference at line %d\n  on disk:   %s\n  generated: %s", apidoc.DocPath, i+1, g, w)
		}
	}
}
