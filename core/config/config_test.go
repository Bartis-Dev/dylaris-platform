package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeDBType(t *testing.T) {
	cases := map[string]string{
		"timescaledb": "timescaledb",
		"TimescaleDB": "timescaledb",
		" timescale ": "timescaledb",
		"ts":          "timescaledb",
		"postgres":    "postgres",
		"PostgreSQL":  "postgres",
		"pg":          "postgres",
		"plain":       "postgres",
		"":            "postgres", // unknown/empty -> safer plain backend
		"mysql":       "postgres", // unknown -> plain
	}
	for in, want := range cases {
		if got := NormalizeDBType(in); got != want {
			t.Errorf("NormalizeDBType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUsesTimescale(t *testing.T) {
	if !UsesTimescale("timescaledb") {
		t.Error("UsesTimescale(timescaledb) = false, want true")
	}
	if !UsesTimescale("TIMESCALE") {
		t.Error("UsesTimescale(TIMESCALE) = false, want true")
	}
	if UsesTimescale("postgres") {
		t.Error("UsesTimescale(postgres) = true, want false")
	}
	if UsesTimescale("") {
		t.Error("UsesTimescale(empty) = true, want false (defaults to plain)")
	}
}

// TestLoadConfig_RejectsPlaceholderSecrets locks in the fail-closed boot
// guard: LoadConfig must refuse to start with an unset or still-default
// JWT_SECRET/CLUSTER_SECRET, since either would make sessions or
// inter-service auth forgeable. See the Pre-flight note: core/config/ has
// no .env file, so godotenv.Load() is a no-op here and t.Setenv is
// authoritative.
func TestLoadConfig_RejectsPlaceholderSecrets(t *testing.T) {
	cases := []struct {
		name          string
		jwtSecret     string
		clusterSecret string
		wantErrSubstr string
	}{
		{"jwt secret unset (empty)", "", "a-real-cluster-secret-value", "JWT_SECRET"},
		{"jwt secret still the default placeholder", "change-this-secret", "a-real-cluster-secret-value", "JWT_SECRET"},
		{"cluster secret unset (empty)", "a-real-jwt-secret-value", "", "CLUSTER_SECRET"},
		{"cluster secret still the default placeholder", "a-real-jwt-secret-value", "dylaris-cluster-secret", "CLUSTER_SECRET"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JWT_SECRET", tc.jwtSecret)
			t.Setenv("CLUSTER_SECRET", tc.clusterSecret)
			_, err := LoadConfig()
			if err == nil {
				t.Fatal("expected LoadConfig to reject the placeholder/empty secret, got nil error")
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErrSubstr)
			}
		})
	}
}

func TestLoadConfig_AcceptsRealSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "a-real-random-jwt-secret-value")
	t.Setenv("CLUSTER_SECRET", "a-real-random-cluster-secret-value")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.JWTSecret != "a-real-random-jwt-secret-value" {
		t.Errorf("JWTSecret = %q", cfg.JWTSecret)
	}
	if cfg.ClusterSecret != "a-real-random-cluster-secret-value" {
		t.Errorf("ClusterSecret = %q", cfg.ClusterSecret)
	}
}

// TestGetSecret_FilePrecedence locks in the Docker/Portainer secrets
// precedence: <KEY>_FILE (trimmed) wins over the plain <KEY> env var, an
// unreadable/empty file falls through to the plain env var, and with
// neither set the fallback is used.
func TestGetSecret_FilePrecedence(t *testing.T) {
	t.Run("file takes precedence over plain env", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "secret")
		if err := os.WriteFile(path, []byte("  file-value  \n"), 0o600); err != nil {
			t.Fatalf("write temp secret file: %v", err)
		}
		t.Setenv("TESTSECRET_FILE", path)
		t.Setenv("TESTSECRET", "env-value")
		if got := getSecret("TESTSECRET", "fallback-value"); got != "file-value" {
			t.Errorf("getSecret = %q, want %q", got, "file-value")
		}
	})

	t.Run("missing file falls back to plain env", func(t *testing.T) {
		t.Setenv("TESTSECRET_FILE", filepath.Join(t.TempDir(), "does-not-exist"))
		t.Setenv("TESTSECRET", "env-value")
		if got := getSecret("TESTSECRET", "fallback-value"); got != "env-value" {
			t.Errorf("getSecret = %q, want %q", got, "env-value")
		}
	})

	t.Run("empty file falls back to plain env", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "empty-secret")
		if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
			t.Fatalf("write temp secret file: %v", err)
		}
		t.Setenv("TESTSECRET_FILE", path)
		t.Setenv("TESTSECRET", "env-value")
		if got := getSecret("TESTSECRET", "fallback-value"); got != "env-value" {
			t.Errorf("getSecret = %q, want %q", got, "env-value")
		}
	})

	t.Run("neither set returns the fallback", func(t *testing.T) {
		if got := getSecret("TESTSECRET", "fallback-value"); got != "fallback-value" {
			t.Errorf("getSecret = %q, want %q", got, "fallback-value")
		}
	})

	t.Run("plain env only", func(t *testing.T) {
		t.Setenv("TESTSECRET", "env-only-value")
		if got := getSecret("TESTSECRET", "fallback-value"); got != "env-only-value" {
			t.Errorf("getSecret = %q, want %q", got, "env-only-value")
		}
	})
}

func TestResolveTabProxyOrigin(t *testing.T) {
	cases := []struct {
		name        string
		rawOrigin   string
		frontendURL string
		wantOrigin  string
		wantActive  bool
	}{
		{"empty origin disables", "", "https://mc.example.com", "", false},
		{"same host different port active", "https://mc.example.com:25502", "https://mc.example.com", "https://mc.example.com:25502", true},
		// B5 fix: a proxy origin IDENTICAL to the panel origin is not isolation -
		// a proxied container's JS would run on the panel origin and could read the
		// panel token. Same effective port (incl. the default) must DISABLE.
		{"same host no port disabled (same effective origin)", "https://mc.example.com", "https://mc.example.com", "", false},
		{"same host explicit different ports active", "https://mc.example.com:8443", "https://mc.example.com:25510", "https://mc.example.com:8443", true},
		{"same host explicit 443 vs default https disabled", "https://mc.example.com:443", "https://mc.example.com", "", false},
		{"scheme mismatch same host different port disabled", "http://mc.example.com:25502", "https://mc.example.com", "", false},
		{"host mismatch disables", "https://proxy.other.com:25502", "https://mc.example.com", "", false},
		{"frontend has port, origin host-matches", "http://localhost:25502", "http://localhost:25510", "http://localhost:25502", true},
		{"non-http scheme disables", "ftp://mc.example.com:25502", "https://mc.example.com", "", false},
		{"unparseable origin disables", "://bad", "https://mc.example.com", "", false},
		{"origin missing host disables", "http://", "https://mc.example.com", "", false},
		{"case-insensitive host match", "https://MC.example.com:25502", "https://mc.example.com", "https://MC.example.com:25502", true},
		{"whitespace trimmed", "  https://mc.example.com:25502  ", "https://mc.example.com", "https://mc.example.com:25502", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotOrigin, gotActive := resolveTabProxyOrigin(tc.rawOrigin, tc.frontendURL)
			if gotOrigin != tc.wantOrigin || gotActive != tc.wantActive {
				t.Errorf("resolveTabProxyOrigin(%q, %q) = (%q, %v), want (%q, %v)",
					tc.rawOrigin, tc.frontendURL, gotOrigin, gotActive, tc.wantOrigin, tc.wantActive)
			}
		})
	}
}
