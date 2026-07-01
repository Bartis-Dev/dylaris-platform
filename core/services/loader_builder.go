package services

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrLoaderDeferred marks loader types not yet buildable in this phase
// (Forge/NeoForge need a Docker container — a follow-up sub-phase).
var ErrLoaderDeferred = errors.New("loader build not yet supported for this loader")

// BuildLoaderArtifact builds the launcher-side loader zip for the given triple.
// For Fabric/Quilt the zip has a single entry bin/version.json (the loader's
// profile JSON verbatim). Returns the zip bytes and the resolved loader version
// (equal to loaderVersion unless it was empty and latest-stable was resolved).
func BuildLoaderArtifact(ctx context.Context, minecraft, loader, loaderVersion string) ([]byte, string, error) {
	switch loader {
	case "fabric":
		return buildFabricLoader(ctx, minecraft, loaderVersion)
	case "quilt":
		return buildQuiltLoader(ctx, minecraft, loaderVersion) // implemented in Task 3
	case "forge", "neoforge":
		return nil, "", ErrLoaderDeferred
	default:
		return nil, "", fmt.Errorf("unknown loader %q", loader)
	}
}

func buildFabricLoader(ctx context.Context, minecraft, loaderVersion string) ([]byte, string, error) {
	resolved := loaderVersion
	if resolved == "" {
		v, err := resolveLatestStableFabric(ctx, minecraft)
		if err != nil {
			return nil, "", err
		}
		resolved = v
	}
	url := fmt.Sprintf("https://meta.fabricmc.net/v2/versions/loader/%s/%s/profile/json", minecraft, resolved)
	profile, err := fetchLoaderProfile(ctx, url)
	if err != nil {
		return nil, "", err
	}
	zipBytes, err := zipLoaderProfile(profile)
	if err != nil {
		return nil, "", err
	}
	return zipBytes, resolved, nil
}

// zipLoaderProfile wraps the profile JSON as a single-entry zip (bin/version.json),
// the launcher-reserved form for Fabric/Quilt.
func zipLoaderProfile(profileJSON []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("bin/version.json")
	if err != nil {
		_ = zw.Close()
		return nil, err
	}
	if _, err := w.Write(profileJSON); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// loaderMD5 is the hex MD5 over the complete zip bytes (Solder differential-cache key).
func loaderMD5(zipBytes []byte) string {
	sum := md5.Sum(zipBytes)
	return hex.EncodeToString(sum[:])
}

// buildQuiltLoader is a stub — implemented in Task 3.
func buildQuiltLoader(ctx context.Context, minecraft, loaderVersion string) ([]byte, string, error) {
	return nil, "", errors.New("quilt loader build implemented in task 3")
}
