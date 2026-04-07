package main

import (
	"bytes"
	"testing"
)

func TestExtractMOTD(t *testing.T) {
	// string input
	if got := extractMOTD("A Minecraft Server"); got != "A Minecraft Server" {
		t.Errorf("string input: got %q", got)
	}
	// map with "text" key
	if got := extractMOTD(map[string]interface{}{"text": "My Server"}); got != "My Server" {
		t.Errorf("map with text: got %q", got)
	}
	// map without "text"
	if got := extractMOTD(map[string]interface{}{"extra": "foo"}); got != "" {
		t.Errorf("map without text: got %q", got)
	}
	// non-string/non-map type
	if got := extractMOTD(42); got != "" {
		t.Errorf("int input: got %q", got)
	}
	// nil
	if got := extractMOTD(nil); got != "" {
		t.Errorf("nil input: got %q", got)
	}
}

func TestParsePort(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"25565", 25565},
		{"19132", 19132},
		{"0", 25565},   // zero → default 25565
		{"", 25565},    // empty → default 25565
		{"abc", 25565}, // non-numeric → default 25565
	}
	for _, c := range cases {
		if got := parsePort(c.input); got != c.want {
			t.Errorf("parsePort(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestVarIntRoundTrip(t *testing.T) {
	values := []int{0, 1, 127, 128, 255, 16383, 2097151}
	for _, v := range values {
		var buf bytes.Buffer
		writeVarInt(&buf, v)
		got, err := readVarInt(&buf)
		if err != nil {
			t.Fatalf("readVarInt(%d): %v", v, err)
		}
		if got != v {
			t.Errorf("VarInt roundtrip(%d) = %d", v, got)
		}
	}
}

func TestWriteReadMCString(t *testing.T) {
	cases := []string{"", "hello", "minecraft server", "unicode: 日本語"}
	for _, s := range cases {
		var buf bytes.Buffer
		writeString(&buf, s)
		got, err := readMCString(&buf)
		if err != nil {
			t.Fatalf("readMCString(%q): %v", s, err)
		}
		if got != s {
			t.Errorf("MCString roundtrip(%q) = %q", s, got)
		}
	}
}
