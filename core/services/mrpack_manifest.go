package services

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
)

// MrpackEntry is one Modrinth-identified file from an .mrpack manifest.
type MrpackEntry struct {
	ProjectID string
	VersionID string
	FileName  string // base of the manifest file path
	Side      string // "client" | "server" | "both", derived from env
}

// mrpackManifestFile mirrors the subset of a modrinth.index.json files[] entry
// the cross-check snapshot needs. Same shape the node reads
// (node/installer_modpack.go) and Core writes (packs_mrpack.go).
type mrpackManifestFile struct {
	Path      string            `json:"path"`
	Env       map[string]string `json:"env"`
	Downloads []string          `json:"downloads"`
}

type mrpackManifest struct {
	Files []mrpackManifestFile `json:"files"`
}

// maxMrpackIndexJSONBytes bounds the DECOMPRESSED size of modrinth.index.json
// read out of an .mrpack archive. The archive itself is capped at 200MiB
// compressed elsewhere (a separate, pre-existing check); without THIS cap a
// crafted entry with a high compression ratio can decompress to tens/hundreds
// of GB and OOM Core while json.Decode reads it. 8 MiB is generous even for a
// very large modpack's manifest (typically a few hundred KB).
const maxMrpackIndexJSONBytes = 8 << 20

// ParseMrpackContents reads modrinth.index.json from the .mrpack bytes and
// returns one entry per file whose first download is a
// cdn.modrinth.com/data/<project>/versions/<version>/<file> URL. Files without
// such a URL (non-Modrinth downloads) are skipped, not an error; only the
// absence of modrinth.index.json or malformed JSON is an error.
func ParseMrpackContents(mrpack []byte) ([]MrpackEntry, error) {
	zr, err := zip.NewReader(bytes.NewReader(mrpack), int64(len(mrpack)))
	if err != nil {
		return nil, fmt.Errorf("open mrpack zip: %w", err)
	}
	var manifest *mrpackManifest
	for _, f := range zr.File {
		if f.Name != "modrinth.index.json" {
			continue
		}
		if f.UncompressedSize64 > uint64(maxMrpackIndexJSONBytes) {
			return nil, fmt.Errorf("modrinth.index.json exceeds the %d byte cap", maxMrpackIndexJSONBytes)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open modrinth.index.json: %w", err)
		}
		var m mrpackManifest
		decErr := json.NewDecoder(io.LimitReader(rc, int64(maxMrpackIndexJSONBytes)+1)).Decode(&m)
		rc.Close()
		if decErr != nil {
			return nil, fmt.Errorf("decode modrinth.index.json: %w", decErr)
		}
		manifest = &m
		break
	}
	if manifest == nil {
		return nil, fmt.Errorf("modrinth.index.json not found in archive")
	}

	entries := make([]MrpackEntry, 0, len(manifest.Files))
	for _, f := range manifest.Files {
		if len(f.Downloads) == 0 {
			continue
		}
		projectID, versionID, ok := parseModrinthCDNURL(f.Downloads[0])
		if !ok {
			continue // non-Modrinth download: cannot derive project/version
		}
		entries = append(entries, MrpackEntry{
			ProjectID: projectID,
			VersionID: versionID,
			FileName:  path.Base(f.Path),
			Side:      sideFromEnv(f.Env),
		})
	}
	return entries, nil
}

// parseModrinthCDNURL extracts the project and version id from a Modrinth CDN
// download URL of the form
//
//	https://cdn.modrinth.com/data/<PROJECT>/versions/<VERSION>/<file>
//
// It returns ok=false for any URL not on cdn.modrinth.com or not matching that
// path layout. The host allowlist is what makes this path-parse reliable
// (node/installer_modpack.go:55-64).
func parseModrinthCDNURL(rawURL string) (projectID, versionID string, ok bool) {
	const prefix = "https://cdn.modrinth.com/data/"
	if !strings.HasPrefix(rawURL, prefix) {
		return "", "", false
	}
	// rest == "<PROJECT>/versions/<VERSION>/<file...>"
	rest := strings.TrimPrefix(rawURL, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) < 4 || parts[1] != "versions" {
		return "", "", false
	}
	projectID, versionID = parts[0], parts[2]
	if projectID == "" || versionID == "" {
		return "", "", false
	}
	return projectID, versionID, true
}

// sideFromEnv inverts envForSide (packs_mrpack.go:41-50): it maps a manifest env
// block back to a side. client=required + server=unsupported -> "client";
// client=unsupported + server=required -> "server"; anything else -> "both".
func sideFromEnv(env map[string]string) string {
	client, server := env["client"], env["server"]
	switch {
	case client == "required" && server == "unsupported":
		return "client"
	case client == "unsupported" && server == "required":
		return "server"
	default:
		return "both"
	}
}
