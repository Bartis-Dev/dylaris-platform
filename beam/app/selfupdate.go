// Self-apply flow for the beam desktop updater (Phase 3): stream the signed
// binary, then (Task 4) apply it via github.com/minio/selfupdate after OUR code
// has verified it, then (Task 5) relaunch. Verification lives in updater.go and
// always runs BEFORE applyUpdate; this file never makes a trust decision.
package main

import (
	"fmt"
	"io"
	"net/http"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// maxUpdateBytes caps an update download so a poisoned/redirected manifest url
// cannot make the app read an unbounded stream into memory. The beam binary is
// a few tens of MB; 200 MiB is a generous ceiling.
const maxUpdateBytes = 200 << 20

// downloadUpdate streams the update binary from url into memory, emitting
// "update:progress" {loaded,total} per chunk. It is a DISTINCT event from the
// file-transfer "download:progress" so the two never clash. Uses NO short
// timeout (the body is multi-MB) and enforces maxUpdateBytes so the read stays
// bounded. Mirrors the core_client.go streaming pattern; the full bytes are
// returned because the caller must hash + verify them before applying.
func (a *App) downloadUpdate(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{}).Do(req) // no timeout: large binary
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update download failed: HTTP %d", resp.StatusCode)
	}

	total := resp.ContentLength // -1 when the server sends no Content-Length
	buf := make([]byte, 0, 1<<20)
	chunk := make([]byte, 64<<10)
	var loaded int64
	for {
		n, rerr := resp.Body.Read(chunk)
		if n > 0 {
			if loaded+int64(n) > maxUpdateBytes {
				return nil, fmt.Errorf("update download exceeds %d-byte limit", maxUpdateBytes)
			}
			buf = append(buf, chunk[:n]...)
			loaded += int64(n)
			if a.ctx != nil {
				wailsruntime.EventsEmit(a.ctx, "update:progress", map[string]interface{}{
					"loaded": loaded,
					"total":  total,
				})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
	}
	return buf, nil
}
