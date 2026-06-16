package config

import "testing"

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
