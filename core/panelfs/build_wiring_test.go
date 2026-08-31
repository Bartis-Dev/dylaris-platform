package panelfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The panel is compiled into Core, and whether it actually IS is decided by one
// build arg in two files. Nothing else fails when they are wrong.
//
// This was not hypothetical. The Dockerfile's own comment said "Core passes
// PANEL_STAGE=panel-build" while neither CI nor the compose overlay passed
// anything, so the default (panel-none) won and every published Core image
// carried the placeholder instead of the panel. Everything was green: the Go
// tests pass either way, the image builds, Core boots, the API works.
//
// The only signal was a log line at startup, which nobody reads on a machine
// that looks healthy. So the wiring is asserted here instead, where a dropped
// argument fails the build that dropped it.
func TestCIBuildsCoreWithThePanel(t *testing.T) {
	files := map[string]struct {
		mustContain string
		// optional marks a file that is gitignored, so it exists on a
		// developer's machine and not in CI. Checking it where it IS present is
		// worth having; demanding it everywhere would fail every CI run.
		optional bool
	}{
		filepath.Join("..", "..", ".github", "workflows", "ci.yml"): {mustContain: "build-core"},
		filepath.Join("..", "..", "docker-compose.local.yml"):       {mustContain: "services", optional: true},
	}
	for path, want := range files {
		mustContain := want.mustContain
		optional := want.optional
		t.Run(filepath.Base(path), func(t *testing.T) {
			b, err := os.ReadFile(path)
			if err != nil {
				if optional && os.IsNotExist(err) {
					t.Skip("not present here; it is gitignored and only exists on a machine that runs the test bed")
				}
				t.Fatalf("read %s: %v", path, err)
			}
			body := string(b)
			if !strings.Contains(body, mustContain) {
				t.Fatalf("%s does not look like the file this test was written against", path)
			}
			if !strings.Contains(body, "PANEL_STAGE") || !strings.Contains(body, "panel-build") {
				t.Errorf("%s does not pass PANEL_STAGE=panel-build, so the Core it builds "+
					"carries the placeholder bundle and serves no panel at all", path)
			}
		})
	}
}

// And the default stays the SAFE-to-forget one only because the two files above
// are checked. If that ever stops being true, this is the other half of the
// argument: the Dockerfile must keep documenting which stage Core needs.
func TestDockerfileDeclaresThePanelStages(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	body := string(b)
	for _, want := range []string{"AS panel-build", "AS panel-none", "FROM ${PANEL_STAGE} AS panel"} {
		if !strings.Contains(body, want) {
			t.Errorf("Dockerfile no longer contains %q; the build-arg indirection changed shape "+
				"and TestCIBuildsCoreWithThePanel is now checking a value nothing reads", want)
		}
	}
}
