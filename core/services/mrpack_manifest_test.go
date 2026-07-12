package services

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildMrpackZip creates an in-memory .mrpack archive containing only
// modrinth.index.json with the given raw JSON body.
func buildMrpackZip(t *testing.T, indexJSON string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("modrinth.index.json")
	if err != nil {
		t.Fatalf("create modrinth.index.json entry: %v", err)
	}
	if _, err := w.Write([]byte(indexJSON)); err != nil {
		t.Fatalf("write modrinth.index.json: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}

const validMrpackIndex = `{
  "files": [
    {
      "path": "mods/example-mod-1.0.0.jar",
      "env": {"client": "required", "server": "unsupported"},
      "downloads": ["https://cdn.modrinth.com/data/AANobbMI/versions/abcdef12/example-mod-1.0.0.jar"]
    },
    {
      "path": "mods/server-only-mod.jar",
      "env": {"client": "unsupported", "server": "required"},
      "downloads": ["https://cdn.modrinth.com/data/P7dR8mSH/versions/xyz98765/server-only-mod.jar"]
    },
    {
      "path": "mods/both-sides-mod.jar",
      "env": {"client": "required", "server": "required"},
      "downloads": ["https://cdn.modrinth.com/data/gvQqBUqZ/versions/qwerty00/both-sides-mod.jar"]
    },
    {
      "path": "mods/external-mod.jar",
      "env": {"client": "required", "server": "required"},
      "downloads": ["https://example.com/not-modrinth/external-mod.jar"]
    },
    {
      "path": "config/some.cfg",
      "env": {"client": "optional", "server": "optional"},
      "downloads": []
    }
  ]
}`

func TestParseMrpackContents_Valid(t *testing.T) {
	zipBytes := buildMrpackZip(t, validMrpackIndex)
	entries, err := ParseMrpackContents(zipBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// external-mod.jar (non-Modrinth download) and config/some.cfg (no
	// downloads) are skipped, not errors - only the 3 Modrinth-CDN entries
	// come back.
	want := []MrpackEntry{
		{ProjectID: "AANobbMI", VersionID: "abcdef12", FileName: "example-mod-1.0.0.jar", Side: "client"},
		{ProjectID: "P7dR8mSH", VersionID: "xyz98765", FileName: "server-only-mod.jar", Side: "server"},
		{ProjectID: "gvQqBUqZ", VersionID: "qwerty00", FileName: "both-sides-mod.jar", Side: "both"},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i, e := range entries {
		if e != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, e, want[i])
		}
	}
}

func TestParseMrpackContents_MissingManifest(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("some-other-file.txt")
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if _, err := w.Write([]byte("not the manifest")); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}

	_, err = ParseMrpackContents(buf.Bytes())
	if err == nil || !strings.Contains(err.Error(), "modrinth.index.json not found") {
		t.Fatalf("expected a not-found error, got %v", err)
	}
}

func TestParseMrpackContents_MalformedJSON(t *testing.T) {
	zipBytes := buildMrpackZip(t, `{"files": [not valid json`)
	_, err := ParseMrpackContents(zipBytes)
	if err == nil || !strings.Contains(err.Error(), "decode modrinth.index.json") {
		t.Fatalf("expected a decode error, got %v", err)
	}
}

func TestParseMrpackContents_UnreadableZip(t *testing.T) {
	_, err := ParseMrpackContents([]byte("not a zip file"))
	if err == nil || !strings.Contains(err.Error(), "open mrpack zip") {
		t.Fatalf("expected an open-zip error, got %v", err)
	}
}

func TestParseMrpackContents_IndexJSONBombCapRejected(t *testing.T) {
	// modrinth.index.json here is a validly-structured but oversized JSON
	// document (8MiB+1 padding in an unused field). ParseMrpackContents
	// must reject it based on the zip entry's declared UncompressedSize64,
	// before ever handing it to json.Decode.
	padding := strings.Repeat("a", maxMrpackIndexJSONBytes+1)
	oversized := `{"files":[],"padding":"` + padding + `"}`
	zipBytes := buildMrpackZip(t, oversized)

	_, err := ParseMrpackContents(zipBytes)
	if err == nil || !strings.Contains(err.Error(), "exceeds the") {
		t.Fatalf("expected a byte-cap error, got %v", err)
	}
}

func TestParseModrinthCDNURL(t *testing.T) {
	cases := []struct {
		name        string
		url         string
		wantProject string
		wantVersion string
		wantOK      bool
	}{
		{
			"valid cdn url",
			"https://cdn.modrinth.com/data/AANobbMI/versions/abcdef12/example-mod-1.0.0.jar",
			"AANobbMI", "abcdef12", true,
		},
		{
			"valid cdn url with a nested file path",
			"https://cdn.modrinth.com/data/AANobbMI/versions/abcdef12/subdir/mod.jar",
			"AANobbMI", "abcdef12", true,
		},
		{"non-modrinth host", "https://example.com/data/AANobbMI/versions/abcdef12/mod.jar", "", "", false},
		{"missing versions segment", "https://cdn.modrinth.com/data/AANobbMI/xyz/abcdef12/mod.jar", "", "", false},
		{"too few path segments", "https://cdn.modrinth.com/data/AANobbMI/versions/", "", "", false},
		{"empty project id", "https://cdn.modrinth.com/data//versions/abcdef12/mod.jar", "", "", false},
		{"plain non-url string", "not-a-url-at-all", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project, version, ok := parseModrinthCDNURL(tc.url)
			if ok != tc.wantOK || project != tc.wantProject || version != tc.wantVersion {
				t.Errorf("parseModrinthCDNURL(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.url, project, version, ok, tc.wantProject, tc.wantVersion, tc.wantOK)
			}
		})
	}
}

func TestSideFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"client required, server unsupported -> client", map[string]string{"client": "required", "server": "unsupported"}, "client"},
		{"client unsupported, server required -> server", map[string]string{"client": "unsupported", "server": "required"}, "server"},
		{"both required -> both", map[string]string{"client": "required", "server": "required"}, "both"},
		{"both optional -> both", map[string]string{"client": "optional", "server": "optional"}, "both"},
		{"empty map -> both", map[string]string{}, "both"},
		{"nil map -> both", nil, "both"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sideFromEnv(tc.env); got != tc.want {
				t.Errorf("sideFromEnv(%+v) = %q, want %q", tc.env, got, tc.want)
			}
		})
	}
}
