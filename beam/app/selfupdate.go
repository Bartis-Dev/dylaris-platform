// Self-apply flow for the beam desktop updater (Phase 3): stream the signed
// binary, then (Task 4) apply it via github.com/minio/selfupdate after OUR code
// has verified it, then (Task 5) relaunch. Verification lives in updater.go and
// always runs BEFORE applyUpdate; this file never makes a trust decision.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/minio/selfupdate"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// maxUpdateBytes caps an update download so a poisoned/redirected manifest url
// cannot make the app read an unbounded stream into memory. The beam binary is
// a few tens of MB; 200 MiB is a generous ceiling.
const maxUpdateBytes = 200 << 20

// downloadUpdate streams the update binary from url into memory, emitting
// "update:progress" {loaded,total} per chunk. It is a DISTINCT event from the
// file-transfer "download:progress" so the two never clash. Uses a generous
// 15-minute overall context deadline (not a short one) so a stalled connection
// eventually errors out instead of stranding the update flow forever, while
// still enforcing maxUpdateBytes so the read stays bounded. Mirrors the
// core_client.go streaming pattern; the full bytes are returned because the
// caller must hash + verify them before applying.
func (a *App) downloadUpdate(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
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

// applyUpdate atomically replaces the running executable with verified.
// Verification (sha256 + Ed25519, in verifyUpdateBinary) MUST have passed before
// this is called; selfupdate is handed empty Options so it does ONLY the
// OS-specific atomic replace + rollback and is never part of the trust decision.
// On a failed replace the library auto-rolls-back internally; RollbackError
// reports whether that rollback ALSO failed, which we surface distinctly so the
// user knows if a manual reinstall is needed.
func applyUpdate(verified []byte) error {
	if err := selfupdate.Apply(bytes.NewReader(verified), selfupdate.Options{}); err != nil {
		if rbErr := selfupdate.RollbackError(err); rbErr != nil {
			return fmt.Errorf("update failed and rollback failed, reinstall may be needed: %v (rollback: %v)", err, rbErr)
		}
		return fmt.Errorf("update failed, no changes were applied: %w", err)
	}
	return nil
}

// relaunch starts a fresh copy of the (just-updated) executable and asks Wails
// to quit the current one. A plain exec.Start() is enough to outlive the parent
// on both Linux and Windows once we Quit, so no per-OS branch is needed. If the
// spawn fails the update is ALREADY applied, so we return a "restart manually"
// signal instead of failing the whole flow - the new binary is on disk either
// way.
func (a *App) relaunch() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("update applied - please restart manually (cannot locate executable: %v)", err)
	}
	cmd := exec.Command(exe)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("update applied - please restart manually (relaunch failed: %v)", err)
	}
	if a.ctx != nil {
		wailsruntime.Quit(a.ctx)
	} else {
		os.Exit(0)
	}
	return nil
}
