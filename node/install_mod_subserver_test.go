package main

import "testing"

// runInstallMod and runRemoveMod both filepath.Join the payload's
// activeSubServer onto the server's data directory. Join CLEANS "..", it does
// not confine, so a name carrying one walks out of the server root: install
// writes a downloaded jar outside it, remove deletes a file outside it.
//
// The file's header comment says every field is re-checked here because "a stale
// queued payload is technically a stale trust boundary", and targetDir and
// fileName were. activeSubServer was not - and it is the one field whose value
// originates on a node's own disk (.active_server, read back by
// handleInspectOrphan and stored by Core during orphan adoption).
func TestValidActiveSubServerRefusesAnythingThatIsNotOneDirectoryName(t *testing.T) {
	refused := []string{
		"..",
		"../..",
		"../other-tenant",
		"world/../../..",
		"/etc",
		`C:\windows`,
		"a/b",
		`a\b`,
		".",
		"./world",
		"world/",
		" world",
	}
	for _, s := range refused {
		t.Run(s, func(t *testing.T) {
			if validActiveSubServer(s) {
				t.Errorf("validActiveSubServer(%q) = true; it is joined onto the server root and must be a single directory name", s)
			}
		})
	}
}

func TestValidActiveSubServerAcceptsRealNames(t *testing.T) {
	// Empty is a server with no sub-server: its files sit at the root and
	// filepath.Join skips an empty element, so it is a legitimate value.
	accepted := []string{"", "survival", "creative-2", "my_world", "SMP", "world123"}
	for _, s := range accepted {
		t.Run(s, func(t *testing.T) {
			if !validActiveSubServer(s) {
				t.Errorf("validActiveSubServer(%q) = false; this is an ordinary sub-server name", s)
			}
		})
	}
}
