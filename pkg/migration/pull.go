package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Pull downloads the archive at url to destPath, verifying its sha256 matches
// expectedSha256 (case-insensitive hex). The token is sent in the
// `Authorization: Bearer <token>` header — the serve side reads it from the
// same place.
//
// Each attempt downloads into a fresh temp file and only renames it onto
// destPath once the full body has streamed and the hash matched. On a
// transport error or hash mismatch it retries the WHOLE download up to
// maxRetries additional times (so total attempts = maxRetries+1). Returns nil
// only when the bytes on disk hash to the expected value.
func Pull(ctx context.Context, url, token, expectedSha256, destPath string, maxRetries int) error {
	if maxRetries < 0 {
		maxRetries = 0
	}
	want := strings.ToLower(strings.TrimSpace(expectedSha256))

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		got, err := pullOnce(ctx, url, token, destPath)
		if err != nil {
			lastErr = err
			// Context cancellation is terminal — retrying won't help.
			if ctx.Err() != nil {
				return lastErr
			}
			continue
		}
		if got == want {
			return nil
		}
		lastErr = fmt.Errorf("migration: sha256 mismatch (got %s want %s)", got, want)
	}
	return lastErr
}

// pullOnce performs a single download into a fresh temp file alongside
// destPath, computing the sha256 while streaming. On success it renames the
// temp onto destPath and returns the hex digest; on any failure it removes the
// temp and returns the error.
func pullOnce(ctx context.Context, url, token, destPath string) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".migration-pull-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup; on the success path tmp is closed+renamed and this
	// Remove no-ops on the (now absent) temp name.
	defer func() {
		tmp.Close()
		os.Remove(tmpPath)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("migration: pull http status %d", resp.StatusCode)
	}

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
