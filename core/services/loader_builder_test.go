package services

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestBuildLoaderArtifact_UnknownLoader_Error(t *testing.T) {
	_, _, err := BuildLoaderArtifact(context.Background(), "1.20.1", "bogus", "")
	if err == nil {
		t.Fatal("expected error for unknown loader")
	}
}

func TestBuildLoaderArtifact_ForgeAndNeoforge_Deferred(t *testing.T) {
	for _, loader := range []string{"forge", "neoforge"} {
		zipBytes, resolved, err := BuildLoaderArtifact(context.Background(), "1.20.1", loader, "")
		if !errors.Is(err, ErrLoaderDeferred) {
			t.Errorf("%s: err = %v, want ErrLoaderDeferred", loader, err)
		}
		if zipBytes != nil || resolved != "" {
			t.Errorf("%s: expected nil bytes and empty version, got %v %q", loader, zipBytes, resolved)
		}
	}
}

func TestBuildFabricLoader_ResolvesLatestWhenVersionEmpty(t *testing.T) {
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		"https://meta.fabricmc.net/v2/versions/loader/1.20.1":                     {200, `[{"loader":{"version":"0.15.0","stable":true}}]`},
		"https://meta.fabricmc.net/v2/versions/loader/1.20.1/0.15.0/profile/json": {200, `{"id":"fabric-loader-0.15.0-1.20.1"}`},
	}}
	withLoaderMetaClient(t, st)

	zipBytes, resolved, err := BuildLoaderArtifact(context.Background(), "1.20.1", "fabric", "")
	if err != nil {
		t.Fatalf("BuildLoaderArtifact: %v", err)
	}
	if resolved != "0.15.0" {
		t.Errorf("resolved = %q, want 0.15.0", resolved)
	}
	assertSingleEntryZip(t, zipBytes, "bin/version.json", `{"id":"fabric-loader-0.15.0-1.20.1"}`)
}

func TestBuildFabricLoader_UsesGivenVersion_SkipsListCall(t *testing.T) {
	const profileURL = "https://meta.fabricmc.net/v2/versions/loader/1.20.1/0.14.0/profile/json"
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		profileURL: {200, `{"id":"fabric-loader-0.14.0-1.20.1"}`},
	}}
	withLoaderMetaClient(t, st)

	zipBytes, resolved, err := BuildLoaderArtifact(context.Background(), "1.20.1", "fabric", "0.14.0")
	if err != nil {
		t.Fatalf("BuildLoaderArtifact: %v", err)
	}
	if resolved != "0.14.0" {
		t.Errorf("resolved = %q, want the given version 0.14.0 unchanged", resolved)
	}
	assertSingleEntryZip(t, zipBytes, "bin/version.json", `{"id":"fabric-loader-0.14.0-1.20.1"}`)

	for _, u := range st.calls {
		if u != profileURL {
			t.Errorf("unexpected extra request to %s; version list should not be consulted when a version is given", u)
		}
	}
}

func TestBuildQuiltLoader_ResolvesLatestWhenVersionEmpty(t *testing.T) {
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		"https://meta.quiltmc.org/v3/versions/loader/1.20.1":                     {200, `[{"loader":{"version":"0.20.0"}}]`},
		"https://meta.quiltmc.org/v3/versions/loader/1.20.1/0.20.0/profile/json": {200, `{"id":"quilt-loader-0.20.0-1.20.1"}`},
	}}
	withLoaderMetaClient(t, st)

	zipBytes, resolved, err := BuildLoaderArtifact(context.Background(), "1.20.1", "quilt", "")
	if err != nil {
		t.Fatalf("BuildLoaderArtifact: %v", err)
	}
	if resolved != "0.20.0" {
		t.Errorf("resolved = %q, want 0.20.0", resolved)
	}
	assertSingleEntryZip(t, zipBytes, "bin/version.json", `{"id":"quilt-loader-0.20.0-1.20.1"}`)
}

func TestBuildQuiltLoader_ProfileFetchError_Propagates(t *testing.T) {
	st := &stubTransport{byURL: map[string]struct {
		status int
		body   string
	}{
		"https://meta.quiltmc.org/v3/versions/loader/1.20.1/0.20.0/profile/json": {500, "upstream down"},
	}}
	withLoaderMetaClient(t, st)

	zipBytes, resolved, err := BuildLoaderArtifact(context.Background(), "1.20.1", "quilt", "0.20.0")
	if err == nil {
		t.Fatal("expected error when profile fetch fails")
	}
	if zipBytes != nil || resolved != "" {
		t.Errorf("expected nil bytes and empty version on error, got %v %q", zipBytes, resolved)
	}
}

func TestZipLoaderProfile_ProducesSingleEntryZip(t *testing.T) {
	profile := []byte(`{"id":"fabric-loader-0.10.0-1.20.1","mainClass":"net.fabricmc.loader.impl.launch.knot.KnotClient"}`)
	zipBytes, err := zipLoaderProfile(profile)
	if err != nil {
		t.Fatalf("zipLoaderProfile: %v", err)
	}
	assertSingleEntryZip(t, zipBytes, "bin/version.json", string(profile))
}

// assertSingleEntryZip verifies zipBytes is a valid zip with exactly one
// entry at wantName whose content equals wantContent exactly.
func assertSingleEntryZip(t *testing.T, zipBytes []byte, wantName, wantContent string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("invalid zip: %v", err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("zip has %d entries, want 1", len(zr.File))
	}
	f := zr.File[0]
	if f.Name != wantName {
		t.Fatalf("entry name = %q, want %q", f.Name, wantName)
	}
	rc, err := f.Open()
	if err != nil {
		t.Fatalf("open entry: %v", err)
	}
	defer rc.Close()
	content, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if string(content) != wantContent {
		t.Fatalf("entry content = %s, want %s", content, wantContent)
	}
}
