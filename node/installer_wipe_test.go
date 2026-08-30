package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// A modpack update has to remove the OLD pack's mods. Adding the new ones on top
// is how a server ends up booting with two versions of half its mod list, and it
// is the reason this exists at all.
func TestWipeBeforeInstallClearsWhatItIsAskedFor(t *testing.T) {
	dest := t.TempDir()
	writeFile(t, filepath.Join(dest, "mods", "old-mod.jar"), "x")
	writeFile(t, filepath.Join(dest, "config", "old.toml"), "x")
	writeFile(t, filepath.Join(dest, "defaultconfigs", "old.toml"), "x")
	writeFile(t, filepath.Join(dest, "libraries", "net", "lib.jar"), "x")
	writeFile(t, filepath.Join(dest, "world", "level.dat"), "keep me")

	if err := WipeBeforeInstall(dest, []string{WipeMods, WipeConfig, WipeLibraries}); err != nil {
		t.Fatalf("wipe: %v", err)
	}

	for _, gone := range []string{"mods", "config", "defaultconfigs", "libraries"} {
		if exists(filepath.Join(dest, gone)) {
			t.Errorf("%s should have been cleared", gone)
		}
	}
	// The world is never in the vocabulary, so nothing can ask for it.
	if !exists(filepath.Join(dest, "world", "level.dat")) {
		t.Error("the world was deleted; it is not a wipe target and must not be reachable")
	}
}

// "Clear the mods" on a server that has none has already achieved what it asked
// for. Failing would turn a first install with the dialog ticked into an error.
func TestWipeBeforeInstallIgnoresMissingDirectories(t *testing.T) {
	if err := WipeBeforeInstall(t.TempDir(), []string{WipeMods, WipeConfig, WipeJars}); err != nil {
		t.Fatalf("a missing directory must not fail the install: %v", err)
	}
}

// An unknown token is refused rather than skipped.
//
// Skipping would hand the operator a dirty update and tell them it succeeded -
// the exact failure this feature exists to end, now with a confirmation dialog
// in front of it to make it look deliberate.
func TestWipeBeforeInstallRefusesAnUnknownTarget(t *testing.T) {
	dest := t.TempDir()
	writeFile(t, filepath.Join(dest, "mods", "m.jar"), "x")

	err := WipeBeforeInstall(dest, []string{WipeMods, "world"})
	if err == nil {
		t.Fatal("an unknown wipe target must be refused")
	}
	if !strings.Contains(err.Error(), "unknown wipe target") {
		t.Errorf("error = %v, want it to name the unknown target", err)
	}
}

// The wire carries tokens, not paths - so a traversal cannot even be expressed.
// Asserted anyway: this is the one place the node destroys a tenant's files on
// somebody else's say-so, and "it cannot be expressed" is a property of today's
// vocabulary rather than of the function.
func TestWipeBeforeInstallRefusesPathsInsteadOfTokens(t *testing.T) {
	dest := t.TempDir()
	for _, attempt := range []string{"../", "..", "/etc", "mods/../../escape", "world"} {
		if err := WipeBeforeInstall(dest, []string{attempt}); err == nil {
			t.Errorf("%q was accepted as a wipe target", attempt)
		}
	}
}

// Jars are cleared in the ROOT only. A jar deeper in the tree is a mod or a
// library, and those are cleared by their own tokens or deliberately kept.
func TestWipeJarsIsNotRecursive(t *testing.T) {
	dest := t.TempDir()
	writeFile(t, filepath.Join(dest, "server.jar"), "x")
	writeFile(t, filepath.Join(dest, "forge-1.20.jar"), "x")
	writeFile(t, filepath.Join(dest, "eula.txt"), "keep")
	writeFile(t, filepath.Join(dest, "mods", "some-mod.jar"), "keep")

	if err := WipeBeforeInstall(dest, []string{WipeJars}); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	if exists(filepath.Join(dest, "server.jar")) || exists(filepath.Join(dest, "forge-1.20.jar")) {
		t.Error("root jars should have been cleared")
	}
	if !exists(filepath.Join(dest, "eula.txt")) {
		t.Error("a non-jar in the root was deleted")
	}
	if !exists(filepath.Join(dest, "mods", "some-mod.jar")) {
		t.Error("a jar inside mods was deleted; jars is root-only")
	}
}

// The tenant can plant a symlink: the server directory is bind-mounted into
// their own Minecraft container and reachable over SFTP. A link named "mods"
// pointing outside must not turn a routine pack update into a delete of whatever
// it points at.
func TestWipeBeforeInstallRefusesASymlinkedTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows; the guard is shared with the file API, which covers it there")
	}
	dest := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "precious.txt"), "do not delete")

	if err := os.Symlink(outside, filepath.Join(dest, "mods")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := WipeBeforeInstall(dest, []string{WipeMods}); err == nil {
		t.Error("a symlinked wipe target must be refused")
	}
	if !exists(filepath.Join(outside, "precious.txt")) {
		t.Fatal("the symlink target was deleted")
	}
}

func TestIsWipeToken(t *testing.T) {
	for _, ok := range []string{WipeMods, WipeConfig, WipeLibraries, WipeVersions, WipeJars} {
		if !IsWipeToken(ok) {
			t.Errorf("%q should be a known token", ok)
		}
	}
	for _, bad := range []string{"", "world", "logs", "MODS", "mods/"} {
		if IsWipeToken(bad) {
			t.Errorf("%q should not be a known token", bad)
		}
	}
}
