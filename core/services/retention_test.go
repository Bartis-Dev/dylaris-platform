package services

import (
	"testing"
	"time"
)

func TestAddRetention(t *testing.T) {
	base := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		spec   string
		want   time.Time
		wantOK bool
	}{
		{"3d", time.Date(2026, 1, 18, 12, 0, 0, 0, time.UTC), true},
		{"2w", time.Date(2026, 1, 29, 12, 0, 0, 0, time.UTC), true},
		{"3m", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC), true},
		{"0d", base, true},
		{"", base, false},
		{"x", base, false},
		{"3", base, false},
		{"-1d", base, false},
		{"d", base, false},
	}
	for _, c := range cases {
		got, ok := AddRetention(base, c.spec)
		if ok != c.wantOK {
			t.Errorf("AddRetention(%q) ok=%v, want %v", c.spec, ok, c.wantOK)
			continue
		}
		if ok && !got.Equal(c.want) {
			t.Errorf("AddRetention(%q) = %v, want %v", c.spec, got, c.want)
		}
	}
}

func TestValidRetentionSpec(t *testing.T) {
	for _, s := range []string{"3d", "2w", "3m", "10d"} {
		if !ValidRetentionSpec(s) {
			t.Errorf("ValidRetentionSpec(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "x", "3", "3y", "-1d"} {
		if ValidRetentionSpec(s) {
			t.Errorf("ValidRetentionSpec(%q) = true, want false", s)
		}
	}
}
