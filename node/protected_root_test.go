package main

import "testing"

// Every destructive file handler resolves an empty path to the server
// directory, because an empty path legitimately means "list the server
// directory" on the read side. Delete would RemoveAll that directory and the
// backups inside it, rename would move it out from under its UUID, and copy
// would walk the tree onto itself - which is what actually happened here and
// cost a server its world, its configs and both of its backup archives.
//
// isProtectedFile is the single guard all of them consult first, so the root
// belongs in it alongside the dotfiles it already defends.
func TestIsProtectedFileCoversTheServerRoot(t *testing.T) {
	protected := []string{
		"", ".", "/", "./",
		".active_server", ".dylaris.json", ".node_config.json",
		".dylaris-backups", ".dylaris-backups/20260806-105747.tar.gz",
		"survival/.active_server",
		".pending-delete-123",
	}
	for _, p := range protected {
		if !isProtectedFile(p) {
			t.Errorf("isProtectedFile(%q) = false, want true", p)
		}
	}

	allowed := []string{
		"survival", "survival/server.properties", "survival/world/level.dat",
		"survival/plugins", "eula.txt", ".paper",
	}
	for _, p := range allowed {
		if isProtectedFile(p) {
			t.Errorf("isProtectedFile(%q) = true, want false - ordinary files must stay writable", p)
		}
	}
}
