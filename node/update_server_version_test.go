package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Staging is all or nothing.
//
// The version move used to delete the old jars first and then download the new
// ones, skipping past any download that failed. One bad fetch and the mod was
// gone: the server came up on the new Minecraft version without it, the panel
// reported success, and the only trace was a line in the node's log. Nothing is
// removed until every replacement is on disk.
func TestStageInstalls_OneFailedFetchStagesNothing(t *testing.T) {
	dir := t.TempDir()

	// The mod that is being replaced. It must still be here afterwards, because
	// the caller only removes anything once staging has succeeded.
	existing := filepath.Join(dir, "sodium.jar")
	if err := os.WriteFile(existing, []byte("the version that works today"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := []versionUpdateFile{
		{FileName: "sodium.jar", DownloadURL: "https://cdn.modrinth.com/data/a/versions/1/sodium.jar", SHA512: "aa"},
		{FileName: "lithium.jar", DownloadURL: "https://cdn.modrinth.com/data/b/versions/1/lithium.jar", SHA512: "bb"},
	}

	calls := 0
	staged, err := stageInstalls(dir, files, func(_, dst, _ string) error {
		calls++
		if calls == 1 {
			return os.WriteFile(dst, []byte("first one arrived"), 0o644)
		}
		return errors.New("upstream returned 503")
	})
	if err == nil {
		t.Fatal("a failed fetch was reported as a successful staging")
	}
	if staged != nil {
		t.Errorf("returned %d staged jars alongside an error", len(staged))
	}

	// The one that DID arrive is cleaned up: this directory is bind-mounted into
	// the tenant's container, so a half-written attempt must not be left in it.
	if _, statErr := os.Stat(filepath.Join(dir, "sodium.jar.part")); !os.IsNotExist(statErr) {
		t.Error("the successfully fetched jar was left behind as debris")
	}
	// And nothing that was already installed was touched.
	body, readErr := os.ReadFile(existing)
	if readErr != nil || string(body) != "the version that works today" {
		t.Error("staging touched a jar that is currently in use")
	}
}

func TestStageInstalls_SuccessStagesEveryJarBesideItsFinalName(t *testing.T) {
	dir := t.TempDir()
	files := []versionUpdateFile{
		{FileName: "sodium.jar", DownloadURL: "https://cdn.modrinth.com/data/a/versions/1/sodium.jar", SHA512: "aa"},
		{FileName: "lithium.jar", DownloadURL: "https://cdn.modrinth.com/data/b/versions/1/lithium.jar", SHA512: "bb"},
	}

	staged, err := stageInstalls(dir, files, func(_, dst, _ string) error {
		return os.WriteFile(dst, []byte("jar"), 0o644)
	})
	if err != nil {
		t.Fatalf("staging failed: %v", err)
	}
	if len(staged) != len(files) {
		t.Fatalf("staged %d jars, want %d", len(staged), len(files))
	}
	for i, sj := range staged {
		if sj.final != filepath.Join(dir, files[i].FileName) {
			t.Errorf("final path %q does not match %q", sj.final, files[i].FileName)
		}
		if sj.tmp != sj.final+".part" {
			t.Errorf("staged at %q, want %q", sj.tmp, sj.final+".part")
		}
		if _, statErr := os.Stat(sj.tmp); statErr != nil {
			t.Errorf("%s was reported staged but is not on disk", sj.tmp)
		}
		// The final name must NOT exist yet: renaming is the caller's commit
		// step, after the server has been stopped.
		if _, statErr := os.Stat(sj.final); !os.IsNotExist(statErr) {
			t.Errorf("%s was put in place during staging", sj.final)
		}
	}
}

// A name or URL that would not survive validation must be caught before anything
// is fetched, and must abort rather than being skipped. Skipping it is what
// silently changed which mods the tenant ends up with.
func TestStageInstalls_RefusesBadInputWithoutFetching(t *testing.T) {
	tests := []struct {
		name string
		file versionUpdateFile
	}{
		{"a path instead of a file name",
			versionUpdateFile{FileName: "../../escape.jar", DownloadURL: "https://cdn.modrinth.com/data/a/versions/1/x.jar"}},
		{"an empty file name",
			versionUpdateFile{FileName: "", DownloadURL: "https://cdn.modrinth.com/data/a/versions/1/x.jar"}},
		{"a download from somewhere that is not Modrinth",
			versionUpdateFile{FileName: "x.jar", DownloadURL: "https://example.com/x.jar"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			fetched := false
			_, err := stageInstalls(dir, []versionUpdateFile{tt.file}, func(_, _, _ string) error {
				fetched = true
				return nil
			})
			if err == nil {
				t.Fatal("accepted it")
			}
			if fetched {
				t.Error("fetched before validating")
			}
		})
	}
}
