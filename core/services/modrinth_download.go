package services

import (
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// modrinthCDNPrefix is the ONLY host the Solder render will download mod jars from.
// A hard allowlist keeps the render from being coerced into fetching arbitrary URLs
// out of a content entry's stored ModrinthDownloadURL.
const modrinthCDNPrefix = "https://cdn.modrinth.com/"

// modrinthJarMaxBytes caps a single download so a hostile/huge upstream response
// cannot exhaust memory. 512 MiB comfortably covers any legitimate mod/pack file.
const modrinthJarMaxBytes = 512 << 20

// modrinthDownloadClient bounds the fetch so a stalled CDN cannot hang the render.
var modrinthDownloadClient = &http.Client{Timeout: 120 * time.Second}

// DownloadModrinthJar fetches a mod jar from the Modrinth CDN and verifies its
// bytes against the expected hashes before returning them. url MUST have the
// cdn.modrinth.com prefix (hard allowlist). expectedSHA1 is required; expectedSHA512
// is checked only when non-empty. A hash mismatch is a hard error — the caller must
// not persist unverified bytes.
func DownloadModrinthJar(ctx context.Context, url, expectedSHA1, expectedSHA512 string) ([]byte, error) {
	if !strings.HasPrefix(url, modrinthCDNPrefix) {
		return nil, fmt.Errorf("download refused: %q is not a %s URL", url, modrinthCDNPrefix)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := modrinthDownloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("modrinth download %s: status %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, modrinthJarMaxBytes))
	if err != nil {
		return nil, err
	}

	if expectedSHA1 != "" {
		s1 := sha1.Sum(data)
		if got := hex.EncodeToString(s1[:]); !strings.EqualFold(got, expectedSHA1) {
			return nil, fmt.Errorf("sha1 mismatch for %s: got %s want %s", url, got, expectedSHA1)
		}
	}
	if expectedSHA512 != "" {
		s5 := sha512.Sum512(data)
		if got := hex.EncodeToString(s5[:]); !strings.EqualFold(got, expectedSHA512) {
			return nil, fmt.Errorf("sha512 mismatch for %s: got %s want %s", url, got, expectedSHA512)
		}
	}
	return data, nil
}
