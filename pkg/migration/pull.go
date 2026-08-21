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
// maxBytes is the size the source ANNOUNCED for this archive; anything past it
// is refused mid-stream. See pullOnce for why the hash alone is not a bound.
// 0 disables the cap (an older core sends no size).
//
// Each attempt downloads into a fresh temp file and only renames it onto
// destPath once the full body has streamed and the hash matched. On a
// transport error or hash mismatch it retries the WHOLE download up to
// maxRetries additional times (so total attempts = maxRetries+1). Returns nil
// only when the bytes on disk hash to the expected value.
func Pull(ctx context.Context, url, token, expectedSha256, destPath string, maxRetries int, maxBytes int64) error {
	return pull(ctx, url, token, expectedSha256, destPath, maxRetries, maxBytes)
}

// PullURL is Pull for a pre-signed URL (S3/R2): the URL itself carries the
// auth in its query string, so NO Authorization header is sent (an extra header
// can conflict with SigV4 query-string signing). Used by the migration R2
// fallback path — the same streaming download + sha256 verification as Pull, so
// a corrupted transfer never reaches Extract.
func PullURL(ctx context.Context, url, expectedSha256, destPath string, maxRetries int, maxBytes int64) error {
	return pull(ctx, url, "", expectedSha256, destPath, maxRetries, maxBytes)
}

func pull(ctx context.Context, url, token, expectedSha256, destPath string, maxRetries int, maxBytes int64) error {
	if maxRetries < 0 {
		maxRetries = 0
	}
	want := strings.ToLower(strings.TrimSpace(expectedSha256))

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		got, err := pullOnce(ctx, url, token, destPath, maxBytes)
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
//
// maxBytes bounds the body. The sha256 does NOT: it is verified after the last
// byte has already landed on disk, so an endless response fills the target's
// storage path first and fails the check afterwards. That path is shared by
// every tenant on the node, and the node's own disk-full guards then stop
// unrelated servers from starting. The source of a migration is not always
// platform hardware - a BYON node is the customer's own machine, and it is the
// side that decides how many bytes to send - so the announced size has to be
// enforced rather than trusted. Content-Length is not the bound either: it is
// the sender's claim about the same stream.
func pullOnce(ctx context.Context, url, token, destPath string, maxBytes int64) (string, error) {
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
	// Empty token == pre-signed URL: auth rides in the query string, so omit the
	// header (an empty/extra Authorization can break SigV4 query signing).
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("migration: pull http status %d", resp.StatusCode)
	}

	h := sha256.New()
	// The extra byte is the overflow probe: LimitReader alone would silently
	// truncate at the cap and hand back a "successful" short download.
	body := io.Reader(resp.Body)
	if maxBytes > 0 {
		body = io.LimitReader(resp.Body, maxBytes+1)
	}
	n, err := io.Copy(io.MultiWriter(tmp, h), body)
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && n > maxBytes {
		return "", fmt.Errorf("migration: response exceeds the announced %d bytes", maxBytes)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
