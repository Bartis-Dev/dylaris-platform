package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// loaderMetaClient bounds meta.fabricmc.net / meta.quiltmc.org calls so a stalled
// upstream cannot hang the loader build.
var loaderMetaClient = &http.Client{Timeout: 30 * time.Second}

// fetchLoaderProfile GETs a loader's launcher profile JSON and returns the raw
// response bytes verbatim (these become bin/version.json unchanged).
func fetchLoaderProfile(ctx context.Context, profileURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, profileURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := loaderMetaClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // profiles are small
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loader meta %s: status %d: %s", profileURL, resp.StatusCode, truncateProfileErr(body))
	}
	return body, nil
}

func truncateProfileErr(b []byte) string {
	if len(b) > 300 {
		return string(b[:300])
	}
	return string(b)
}

// quiltLoaderEntry is one element of the Quilt loader-versions list.
type quiltLoaderEntry struct {
	Loader struct {
		Version string `json:"version"`
	} `json:"loader"`
}

// resolveLatestStableQuilt returns the newest stable Quilt loader version for a
// game version. Quilt omits a stable flag, so "stable" = version has no -beta suffix.
func resolveLatestStableQuilt(ctx context.Context, gameVersion string) (string, error) {
	url := fmt.Sprintf("https://meta.quiltmc.org/v3/versions/loader/%s", gameVersion)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := loaderMetaClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("quilt loader list %s: status %d", gameVersion, resp.StatusCode)
	}
	var entries []quiltLoaderEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return "", err
	}
	for _, e := range entries {
		if !strings.Contains(strings.ToLower(e.Loader.Version), "-beta") {
			return e.Loader.Version, nil
		}
	}
	if len(entries) > 0 {
		return entries[0].Loader.Version, nil
	}
	return "", fmt.Errorf("no quilt loader versions for %s", gameVersion)
}

// fabricLoaderEntry is one element of the Fabric loader-versions list.
type fabricLoaderEntry struct {
	Loader struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	} `json:"loader"`
}

// resolveLatestStableFabric returns the newest stable Fabric loader version for a
// game version (first entry with stable==true).
func resolveLatestStableFabric(ctx context.Context, gameVersion string) (string, error) {
	url := fmt.Sprintf("https://meta.fabricmc.net/v2/versions/loader/%s", gameVersion)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := loaderMetaClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fabric loader list %s: status %d", gameVersion, resp.StatusCode)
	}
	var entries []fabricLoaderEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Loader.Stable {
			return e.Loader.Version, nil
		}
	}
	if len(entries) > 0 {
		return entries[0].Loader.Version, nil // fall back to newest even if unstable
	}
	return "", fmt.Errorf("no fabric loader versions for %s", gameVersion)
}
