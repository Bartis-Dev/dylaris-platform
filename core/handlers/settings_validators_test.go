package handlers

import (
	"testing"

	sharedxdp "dylaris-pkg/xdp"
)

// TestNormalizeWarpPorts pins the leader-trusted port-list normalizer
// (settings.go): dedupe + numeric sort + range 1..65535, reject any
// non-numeric or out-of-range entry.
func TestNormalizeWarpPorts(t *testing.T) {
	cases := []struct {
		name    string
		csv     string
		want    string
		wantErr bool
	}{
		{"normal csv sorted as-is", "6379,25501,25551", "6379,25501,25551", false},
		{"out-of-order input gets sorted", "25551,6379,25501", "6379,25501,25551", false},
		{"duplicate entries collapse", "6379,6379,25501", "6379,25501", false},
		{"whitespace around entries trimmed", " 6379 , 25501 ", "6379,25501", false},
		{"empty segments skipped", "6379,,25501", "6379,25501", false},
		{"empty input returns empty string, no error", "", "", false},
		{"only empty segments returns empty string, no error", " , ", "", false},
		{"port 0 rejected (below range)", "0,6379", "", true},
		{"port 65536 rejected (above range)", "6379,65536", "", true},
		{"port exactly 1 accepted (boundary)", "1", "1", false},
		{"port exactly 65535 accepted (boundary)", "65535", "65535", false},
		{"non-numeric entry rejected", "6379,abc", "", true},
		{"negative number rejected", "-1", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normalizeWarpPorts(c.csv)
			if (err != nil) != c.wantErr {
				t.Fatalf("normalizeWarpPorts(%q) err = %v, wantErr %v", c.csv, err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("normalizeWarpPorts(%q) = %q, want %q", c.csv, got, c.want)
			}
		})
	}
}

func TestValidHosterValidation(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"letters", true},
		{"alphanumeric", true},
		{"dns", true},
		{"regex", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.v, func(t *testing.T) {
			if got := validHosterValidation(c.v); got != c.want {
				t.Errorf("validHosterValidation(%q) = %v, want %v", c.v, got, c.want)
			}
		})
	}
}

func TestValidBackupMode(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"s3", true},
		{"node-local", true},
		{"shared", true},
		{"local", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.v, func(t *testing.T) {
			if got := validBackupMode(c.v); got != c.want {
				t.Errorf("validBackupMode(%q) = %v, want %v", c.v, got, c.want)
			}
		})
	}
}

func TestValidRoutingMode(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"ip_port", true},
		{"both", true},
		{"gateway", true},
		{"dns", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.v, func(t *testing.T) {
			if got := validRoutingMode(c.v); got != c.want {
				t.Errorf("validRoutingMode(%q) = %v, want %v", c.v, got, c.want)
			}
		})
	}
}

func TestValidFileMode(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"sftp", true},
		{"both", true},
		{"beam", true},
		{"ftp", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.v, func(t *testing.T) {
			if got := validFileMode(c.v); got != c.want {
				t.Errorf("validFileMode(%q) = %v, want %v", c.v, got, c.want)
			}
		})
	}
}

func TestValidMaintenanceLevel(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"off", true},
		{"banner_only", true},
		{"block_writes", true},
		{"block_all", true},
		{"lockdown", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.v, func(t *testing.T) {
			if got := validMaintenanceLevel(c.v); got != c.want {
				t.Errorf("validMaintenanceLevel(%q) = %v, want %v", c.v, got, c.want)
			}
		})
	}
}

// TestValidateXDPConfig pins both the returned error AND the in-place clamp
// mutation (xdp.go). validateXDPConfig mutates cfg even when it ultimately
// returns nil.
func TestValidateXDPConfig(t *testing.T) {
	t.Run("enabled with empty protected ports errors", func(t *testing.T) {
		cfg := &sharedxdp.Config{Enabled: true, ProtectedPorts: ""}
		if err := validateXDPConfig(cfg); err == nil {
			t.Fatalf("expected error for enabled+empty ProtectedPorts, got nil")
		}
	})

	t.Run("enabled with whitespace-only protected ports errors", func(t *testing.T) {
		cfg := &sharedxdp.Config{Enabled: true, ProtectedPorts: "   "}
		if err := validateXDPConfig(cfg); err == nil {
			t.Fatalf("expected error for enabled+whitespace ProtectedPorts, got nil")
		}
	})

	t.Run("enabled with non-empty protected ports is accepted", func(t *testing.T) {
		cfg := &sharedxdp.Config{Enabled: true, ProtectedPorts: "25565"}
		if err := validateXDPConfig(cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("disabled with empty protected ports is accepted (no port-filter guard needed)", func(t *testing.T) {
		cfg := &sharedxdp.Config{Enabled: false, ProtectedPorts: ""}
		if err := validateXDPConfig(cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("zero-value numeric fields are clamped up to their floors", func(t *testing.T) {
		cfg := &sharedxdp.Config{}
		if err := validateXDPConfig(cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RateLimit != 1 {
			t.Errorf("RateLimit = %d, want clamped to 1", cfg.RateLimit)
		}
		if cfg.RateWindowMs != 100 {
			t.Errorf("RateWindowMs = %d, want clamped to 100", cfg.RateWindowMs)
		}
		if cfg.BanDurationMin != 1 {
			t.Errorf("BanDurationMin = %d, want clamped to 1", cfg.BanDurationMin)
		}
	})

	t.Run("RateLimit above 1,000,000 is clamped down", func(t *testing.T) {
		cfg := &sharedxdp.Config{RateLimit: 5_000_000}
		if err := validateXDPConfig(cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RateLimit != 1_000_000 {
			t.Errorf("RateLimit = %d, want clamped to 1000000", cfg.RateLimit)
		}
	})

	t.Run("RateLimit exactly at the cap is left untouched", func(t *testing.T) {
		cfg := &sharedxdp.Config{RateLimit: 1_000_000}
		if err := validateXDPConfig(cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RateLimit != 1_000_000 {
			t.Errorf("RateLimit = %d, want unchanged 1000000", cfg.RateLimit)
		}
	})

	t.Run("in-range non-zero values are left untouched", func(t *testing.T) {
		cfg := &sharedxdp.Config{RateLimit: 500, RateWindowMs: 50, BanDurationMin: 5}
		if err := validateXDPConfig(cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RateLimit != 500 || cfg.RateWindowMs != 50 || cfg.BanDurationMin != 5 {
			t.Errorf("cfg = %+v, want unchanged (500, 50, 5)", cfg)
		}
	})
}
