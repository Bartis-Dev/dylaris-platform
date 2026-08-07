package main

import (
	"os"
	"regexp"
	"sync"
	"testing"
)

// The command consumer runs 8 handlers in parallel, and the queue is
// at-least-once, so a redelivery lands NEXT TO the original rather than after
// it. Every destructive per-server command therefore needs a duplicate guard,
// not just backup/restore (which have one keyed on their run id).
//
// Observed on the testbed with two setups of one server, both accepted:
//
//	20:05:01 Setting up server ..._dblsetup01 (sub-server: survival)...
//	20:05:01 Starting Installation: vanilla (1.21.4) -> .../survival
//	20:05:01 Setting up server ..._dblsetup01 (sub-server: survival)...
//	20:05:01 Starting Installation: vanilla (1.21.4) -> .../survival
//
// - two installers writing one directory in the same second, then both
// recreating the container; one won and logged "deployed and running", the
// other logged "Failed to start server pod ... name is already in use".
func TestDestructiveCommandsGuardAgainstADuplicate(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(src)

	for _, action := range []string{"setup", "reinstall", "migrate_storage"} {
		t.Run(action, func(t *testing.T) {
			caseRe := regexp.MustCompile(`(?s)case "` + action + `":(.*?)\n\tcase "`)
			m := caseRe.FindStringSubmatch(text)
			if m == nil {
				t.Fatalf("case %q not found in the command switch", action)
			}
			want := regexp.MustCompile(`commandsInFlight\.enter\("` + action + `:`)
			if !want.MatchString(m[1]) {
				t.Fatalf("case %q can run twice concurrently for one server: no commandsInFlight guard", action)
			}
		})
	}
}

// The guard's own contract: the second caller is refused, and the slot frees up
// again afterwards so a later deliberate re-run still works.
func TestCommandsInFlightRefusesOnlyTheConcurrentDuplicate(t *testing.T) {
	set := newInflightSet()

	if !set.enter("setup:srv-1") {
		t.Fatal("first caller was refused")
	}
	if set.enter("setup:srv-1") {
		t.Fatal("a concurrent duplicate was admitted")
	}
	// A different server, and a different command for the same server, are
	// independent work and must not be blocked.
	if !set.enter("setup:srv-2") {
		t.Fatal("a different server was blocked")
	}
	if !set.enter("reinstall:srv-1") {
		t.Fatal("a different command for the same server was blocked")
	}

	set.leave("setup:srv-1")
	if !set.enter("setup:srv-1") {
		t.Fatal("the slot did not free up for a later re-run")
	}
}

// Exactly one of N racing callers may win.
func TestCommandsInFlightAdmitsExactlyOneRacer(t *testing.T) {
	set := newInflightSet()
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0

	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if set.enter("setup:srv-race") {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if admitted != 1 {
		t.Fatalf("%d callers admitted, want exactly 1", admitted)
	}
}
