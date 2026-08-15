package main

import "testing"

func TestParseHostNetFromMode(t *testing.T) {
	cases := map[string]bool{
		"host":          true,
		"default":       false,
		"bridge":        false,
		"":              false,
		"container:abc": false,
		"none":          false,
	}
	for mode, want := range cases {
		if got := parseHostNetFromMode(mode); got != want {
			t.Fatalf("parseHostNetFromMode(%q) = %v, want %v", mode, got, want)
		}
	}
}
