package main

import (
	"path/filepath"
	"testing"
)

func TestServerMetadataRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := ServerMetadata{
		Version:         1,
		ServerUUID:      "uuid-1",
		Name:            "My Server",
		MemoryMB:        4096,
		CPULimit:        2.0,
		GameImage:       "eclipse-temurin:21-jre",
		ActiveSubServer: "survival",
		SubServers: []SubServerMetadata{
			{Name: "survival", Type: "forge", MinecraftVersion: "1.20.1", Build: "47.2.0"},
		},
	}
	if err := writeServerMetadata(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := readServerMetadata(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "My Server" || len(out.SubServers) != 1 ||
		out.SubServers[0].Type != "forge" || out.ActiveSubServer != "survival" {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestReadServerMetadataMissing(t *testing.T) {
	if _, err := readServerMetadata(t.TempDir()); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestMetadataFilename(t *testing.T) {
	if metadataPath("/srv") != filepath.Join("/srv", ".dylaris.json") {
		t.Fatal("wrong metadata path")
	}
}
