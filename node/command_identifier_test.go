package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both identifiers in a queued command become directory names on this node, so
// a ".." in either escapes the server root. The dangerous branch is
// delete_sub_server, which ends in os.RemoveAll of the joined path.
//
// The guard sits at the dispatcher, so this asserts the property that makes
// that placement correct: the check happens BEFORE the action switch and
// therefore covers every action, including ones added later.
func TestQueuedIdentifiersAreRejectedBeforeAnyActionRuns(t *testing.T) {
	bad := []struct {
		name       string
		uuid       string
		subServer  string
		wantReason string
	}{
		{"traversing uuid", "../neighbour", "survival", "uuid"},
		{"absolute uuid", "/etc", "survival", "uuid"},
		{"uuid with a redis separator", "aaaa:bbbb:cccc", "survival", "uuid"},
		{"empty uuid", "", "survival", "uuid"},
		{"traversing sub-server", "server-1234abcd", "../..", "sub-server"},
		{"sub-server with a slash", "server-1234abcd", "worlds/../../etc", "sub-server"},
		{"sub-server dotfile", "server-1234abcd", ".dylaris-backups", "sub-server"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if commandIdentifierProblem(c.uuid, c.subServer) == "" {
				t.Errorf("uuid=%q sub=%q was accepted; it names a %s directory that escapes the server root",
					c.uuid, c.subServer, c.wantReason)
			}
		})
	}

	good := []struct{ uuid, subServer string }{
		// The panel mints "<ownerUUID>_<random>", not a canonical UUID.
		{"3f8a2b1c-9d4e-4a7b-8c1d-2e5f6a7b8c9d_x9f2", "survival"},
		{"3f8a2b1c-9d4e-4a7b-8c1d-2e5f6a7b8c9d_x9f2", "creative_2"},
		{"3f8a2b1c-9d4e-4a7b-8c1d-2e5f6a7b8c9d_x9f2", "mod-pack+1"},
		// setup and reinstall default an empty name to "server" themselves.
		{"3f8a2b1c-9d4e-4a7b-8c1d-2e5f6a7b8c9d_x9f2", ""},
	}
	for _, c := range good {
		if problem := commandIdentifierProblem(c.uuid, c.subServer); problem != "" {
			t.Errorf("uuid=%q sub=%q was refused (%s), but it is an ordinary command", c.uuid, c.subServer, problem)
		}
	}
}

// The guard has to be reachable by every action, which means it cannot live in
// a case body. Asserting on the source is the only way to say that: a
// behavioural test can only ever cover the actions that exist today, and the
// failure this prevents is the ELEVENTH action being added without it.
func TestTheIdentifierGuardPrecedesTheActionSwitch(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(".", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(src)

	guard := strings.Index(body, "commandIdentifierProblem(cmd.Config.UUID, cmd.Config.ActiveSubServer)")
	if guard < 0 {
		t.Fatal("the queued-identifier guard is gone from processCommand; " +
			"cmd.Config.UUID becomes a directory name and must be validated")
	}
	sw := strings.Index(body, "switch cmd.Action {")
	if sw < 0 {
		t.Fatal("the action switch moved; this test needs updating with it")
	}
	if guard > sw {
		t.Error("the identifier guard now sits inside the action switch, so it only " +
			"protects the branch it landed in; it belongs before the switch")
	}
}
