package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// MC_RUN_AS is the escape hatch, and every branch of it decides who owns a
// tenant's world - so a typo must not silently turn the hardening off.
func TestMCUser(t *testing.T) {
	cases := []struct {
		name, env string
		wantUID   int
		wantSpec  string
	}{
		{name: "unset", env: "", wantUID: 1000, wantSpec: "1000:1000"},
		{name: "another uid", env: "1500", wantUID: 1500, wantSpec: "1500:1500"},
		// 0 is a deliberate opt-out, not a mistake: an operator with data owned
		// by root who would rather keep it than have every world rewritten.
		{name: "explicit root opt-out", env: "0", wantUID: 0, wantSpec: ""},
		// Garbage falls back to the safe default rather than to root. A typo in
		// an env var must not be the thing that hands a tenant uid 0.
		{name: "not a number", env: "dylaris", wantUID: 1000, wantSpec: "1000:1000"},
		{name: "negative", env: "-1", wantUID: 1000, wantSpec: "1000:1000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MC_RUN_AS", tc.env)
			if got := mcUser(); got != tc.wantUID {
				t.Errorf("mcUser() = %d, want %d", got, tc.wantUID)
			}
			if got := mcUserSpec(); got != tc.wantSpec {
				t.Errorf("mcUserSpec() = %q, want %q", got, tc.wantSpec)
			}
		})
	}
}

// The ownership pass must not touch anything above the sub-server directory.
//
// That boundary IS the feature: .active_server and .dylaris-backups sit in the
// same bind mount the tenant's container gets, and they stay root's precisely so
// a plugin cannot rewrite which sub-server runs or plant a symlink the backup
// download RPC would follow.
func TestOwnershipStopsAtTheSubServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file ownership is a uid concept; the node only runs on Linux")
	}
	if os.Geteuid() != 0 {
		t.Skip("chown needs root; asserted in CI's container and by the live test")
	}
	root := t.TempDir()
	sub := filepath.Join(root, "survival")
	if err := os.MkdirAll(filepath.Join(sub, "world"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, ".active_server")
	if err := os.WriteFile(marker, []byte("survival"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MC_RUN_AS", "1000")
	if err := ensureSubServerOwnership(sub); err != nil {
		t.Fatalf("ensureSubServerOwnership: %v", err)
	}

	fi, err := os.Lstat(sub)
	if err != nil {
		t.Fatal(err)
	}
	if !ownedBy(fi, 1000) {
		t.Error("the sub-server directory was not handed to the container's uid")
	}
	mfi, err := os.Lstat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if ownedBy(mfi, 1000) {
		t.Error(".active_server was handed to the tenant; it must stay root's")
	}
}

// A missing sub-server directory is the normal state of a server that has not
// been installed yet. It must not be an error on the way into a container start.
func TestOwnershipOfSomethingNotInstalledYet(t *testing.T) {
	t.Setenv("MC_RUN_AS", "1000")
	if err := ensureSubServerOwnership(filepath.Join(t.TempDir(), "nothing-here")); err != nil {
		t.Errorf("a server with nothing installed reported an error: %v", err)
	}
}

// Both container-build sites must set the user, and there is no way for a test
// to drive them without a Docker daemon - so this reads the source, the same way
// the panel's build wiring is asserted.
//
// The failure it guards against is silent: forget it at one site and that path
// creates a root container, which works perfectly until a tenant plugin decides
// to write /data/.active_server.
func TestEveryContainerBuildSiteSetsTheUser(t *testing.T) {
	b, err := os.ReadFile("docker_mgr.go")
	if err != nil {
		t.Fatalf("read docker_mgr.go: %v", err)
	}
	src := string(b)

	// Count the MC container configs by their image field, which every one of
	// them sets, and require a User beside each.
	configs := strings.Count(src, "Image:      config.Docker.Image")
	users := strings.Count(src, "User:       mcUserSpec()")
	if configs == 0 {
		t.Fatal("no MC container config found; this test is reading the wrong shape")
	}
	if users != configs {
		t.Errorf("%d MC container configs but %d set User: a container built without it runs as root", configs, users)
	}

	if !strings.Contains(src, "ensureSubServerOwnership(") {
		t.Error("nothing hands the world to the container's uid at start; a non-root server cannot write it")
	}
}
