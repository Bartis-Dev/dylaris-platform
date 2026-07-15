package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveWithinDir pins the path-traversal guard shared by the gRPC file
// handler, the selective-zip download path, and the Beam server. This is the
// single most security-critical helper in the node module.
func TestResolveWithinDir(t *testing.T) {
	t.Run("normal relative path resolves under dataPath", func(t *testing.T) {
		dataPath := filepath.Join("srv", "uuid1")
		got, err := resolveWithinDir(dataPath, filepath.Join("world", "level.dat"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Clean(filepath.Join(dataPath, "world", "level.dat"))
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	t.Run("simple traversal is rejected", func(t *testing.T) {
		dataPath := filepath.Join("srv", "uuid1")
		got, err := resolveWithinDir(dataPath, "../escape")
		if err == nil {
			t.Fatalf("expected error, got path %q", got)
		}
		if got != "" {
			t.Fatalf("expected empty path on error, got %q", got)
		}
	})

	t.Run("deep traversal is rejected", func(t *testing.T) {
		dataPath := filepath.Join("srv", "uuid1")
		if _, err := resolveWithinDir(dataPath, "../../etc/passwd"); err == nil {
			t.Fatal("expected error for ../../etc/passwd")
		}
	})

	// This is the exact scenario called out in the source doc comment: a
	// sibling directory that merely shares dataPath's name as a prefix
	// (uuid1-evil vs uuid1) must NOT be reachable via traversal.
	t.Run("sibling directory sharing a name prefix is rejected", func(t *testing.T) {
		dataPath := filepath.Join("data", "uuid1")
		if _, err := resolveWithinDir(dataPath, filepath.Join("..", "uuid1-evil", "secret")); err == nil {
			t.Fatal("expected error: sibling dir sharing dataPath's prefix must not be reachable")
		}
	})

	// Real behavior: filepath.Join treats a leading "/" in reqPath as just
	// another path segment (only the FIRST argument's absoluteness matters
	// to Join), so an "absolute-looking" reqPath is silently contained
	// inside dataPath rather than escaping to the real filesystem root.
	// This holds on both Windows and Linux since filepath.Join always
	// re-Cleans the joined result.
	t.Run("absolute-looking reqPath is still contained (real Join semantics)", func(t *testing.T) {
		dataPath := filepath.Join("data", "uuid1")
		got, err := resolveWithinDir(dataPath, "/etc/passwd")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cleanData := filepath.Clean(dataPath)
		if got != cleanData && !strings.HasPrefix(got, cleanData+string(filepath.Separator)) {
			t.Fatalf("expected containment, got %q not under %q", got, cleanData)
		}
	})

	t.Run("dot resolves to dataPath itself", func(t *testing.T) {
		dataPath := filepath.Join("data", "uuid1")
		got, err := resolveWithinDir(dataPath, ".")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != filepath.Clean(dataPath) {
			t.Fatalf("got %q want %q", got, filepath.Clean(dataPath))
		}
	})

	t.Run("empty reqPath resolves to dataPath itself", func(t *testing.T) {
		dataPath := filepath.Join("data", "uuid1")
		got, err := resolveWithinDir(dataPath, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != filepath.Clean(dataPath) {
			t.Fatalf("got %q want %q", got, filepath.Clean(dataPath))
		}
	})
}

func TestValidateModrinthURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid cdn url", "https://cdn.modrinth.com/data/abc/versions/xyz/file.jar", false},
		{"http scheme rejected", "http://cdn.modrinth.com/data/abc/versions/xyz/file.jar", true},
		{"other host rejected", "https://evil.example.com/data/abc", true},
		{"empty rejected", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateModrinthURL(c.url)
			if c.wantErr && err == nil {
				t.Fatalf("validateModrinthURL(%q): expected error, got nil", c.url)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("validateModrinthURL(%q): unexpected error: %v", c.url, err)
			}
		})
	}
}

func TestValidSubDir(t *testing.T) {
	cases := []struct {
		dir  string
		want bool
	}{
		{"mods", true},
		{"plugins", true},
		{"config", false},
		{"", false},
		{"Mods", false}, // case-sensitive: exact match only
	}
	for _, c := range cases {
		if got := validSubDir(c.dir); got != c.want {
			t.Errorf("validSubDir(%q) = %v, want %v", c.dir, got, c.want)
		}
	}
}

func TestValidateMrpackURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid modrinth cdn host", "https://cdn.modrinth.com/data/abc/pack.mrpack", false},
		{"valid github host", "https://github.com/owner/repo/releases/download/v1/pack.mrpack", false},
		{"bad scheme rejected", "http://cdn.modrinth.com/data/abc/pack.mrpack", true},
		{"disallowed host rejected", "https://evil.example.com/pack.mrpack", true},
		{"malformed url with no path segment rejected", "https://cdn.modrinth.com", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateMrpackURL(c.url)
			if c.wantErr && err == nil {
				t.Fatalf("validateMrpackURL(%q): expected error, got nil", c.url)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("validateMrpackURL(%q): unexpected error: %v", c.url, err)
			}
		})
	}
}

func TestIsPlatformReservedName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{".active_server", true},
		{".dylaris-backups", true}, // prefix match
		{".dylaris", true},         // exact prefix of itself
		{".DYLARIS", false},        // case-sensitive: must NOT match
		{"eula.txt", false},
		{"server.jar", false},
	}
	for _, c := range cases {
		if got := isPlatformReservedName(c.name); got != c.want {
			t.Errorf("isPlatformReservedName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestBearerToken pins the Authorization: Bearer parse plus the documented
// ?token= query fallback (which exists to keep the endpoint curl-able).
func TestBearerToken(t *testing.T) {
	t.Run("bearer header returns token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/x", nil)
		req.Header.Set("Authorization", "Bearer xyz123")
		if got := bearerToken(req); got != "xyz123" {
			t.Fatalf("got %q want %q", got, "xyz123")
		}
	})

	t.Run("missing header and no query returns empty", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/x", nil)
		if got := bearerToken(req); got != "" {
			t.Fatalf("got %q want empty", got)
		}
	})

	t.Run("non-bearer scheme falls through to empty query", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/x", nil)
		req.Header.Set("Authorization", "Basic abc123")
		if got := bearerToken(req); got != "" {
			t.Fatalf("got %q want empty", got)
		}
	})

	// Real, documented behavior: absent/non-Bearer header falls back to the
	// ?token= query param.
	t.Run("missing header falls back to query token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/x?token=abc123", nil)
		if got := bearerToken(req); got != "abc123" {
			t.Fatalf("got %q want %q", got, "abc123")
		}
	})

	t.Run("bearer header trims surrounding whitespace", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/x", nil)
		req.Header.Set("Authorization", "Bearer   xyz  ")
		if got := bearerToken(req); got != "xyz" {
			t.Fatalf("got %q want %q", got, "xyz")
		}
	})
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{"shorter than n unchanged", "abc", 10, "abc"},
		{"equal to n unchanged", "abcde", 5, "abcde"},
		{"longer than n is cut plus ellipsis", "abcdefghij", 5, "abcde" + "\u2026"},
		{"empty string", "", 5, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := truncate(c.input, c.n); got != c.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", c.input, c.n, got, c.want)
			}
		})
	}
}
