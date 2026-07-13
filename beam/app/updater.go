package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// AppVersion is the running build, injected at build time via
// -ldflags "-X main.AppVersion=X.Y.Z". "dev" for local builds (never nags). The
// updater does an ordered semver compare against the manifest's version.
var AppVersion = "dev"

// manifestURL is the GitHub Releases manifest the app checks for updates. The
// "latest/download" path always resolves to the newest release's fixed-name
// assets. Public once the repo is public (see the Phase 1 plan owner steps). The
// detached signature lives at manifestURL + ".sig".
const manifestURL = "https://github.com/Bartis-Dev/dylaris-platform/releases/latest/download/latest.json"

// errUnverifiedManifest is returned (and swallowed to "no update") when the
// manifest signature does not verify against the embedded public key.
var errUnverifiedManifest = errors.New("beam: manifest signature verification failed")

// UpdateInfo is returned to the frontend to drive the "update available" notice.
type UpdateInfo struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"updateAvailable"`
	DownloadURL     string `json:"downloadUrl"`
}

// manifest is the app's read view of latest.json. The producer writes a
// per-platform url, sha256 (hex, over the binary) and sig (base64 Ed25519 over
// the binary); Phase 3 decodes all three. Integrity is not weakened by decoding
// them: the manifest signature is verified over the FULL raw bytes before any
// field is read, so the sha256/sig become trust-rooted once the manifest sig
// verifies.
type manifest struct {
	Version   string `json:"version"`
	Platforms map[string]struct {
		URL    string `json:"url"`
		Sha256 string `json:"sha256"`
		Sig    string `json:"sig"`
	} `json:"platforms"`
}

// platformArtifact returns the download url, hex sha256, and base64 Ed25519 sig
// for the current runtime.GOOS-runtime.GOARCH from a verified manifest, or an
// error if the manifest carries no entry for this platform (e.g. a Windows
// client before Windows builds exist). The sha256/sig are used only inside the
// Go apply flow and are never surfaced to the frontend.
func platformArtifact(m *manifest) (dlURL, sha256Hex, sigB64 string, err error) {
	key := runtime.GOOS + "-" + runtime.GOARCH
	p, ok := m.Platforms[key]
	if !ok {
		return "", "", "", fmt.Errorf("beam: no update build for platform %s", key)
	}
	return p.URL, p.Sha256, p.Sig, nil
}

// GetUpdateInfo fetches and VERIFIES the signed manifest, then reports whether a
// newer build exists + the download URL for this platform. Best-effort and
// FAIL-CLOSED: any fetch error, a missing/invalid signature, or an unparseable
// embedded key all report "no update" so the app never nags on unverified data
// and never blocks. A nag fires only when latest > current by ordered semver.
func (a *App) GetUpdateInfo() *UpdateInfo {
	info := &UpdateInfo{Current: AppVersion}
	m, err := fetchVerifiedManifest()
	if err != nil {
		return info
	}
	info.Latest = m.Version
	if AppVersion != "dev" && m.Version != "" && semverNewer(m.Version, AppVersion) {
		info.UpdateAvailable = true
	}
	if p, ok := m.Platforms[runtime.GOOS+"-"+runtime.GOARCH]; ok {
		info.DownloadURL = p.URL
	}
	return info
}

// fetchVerifiedManifest downloads latest.json + latest.json.sig and verifies the
// signature over the RAW manifest bytes with the embedded public key BEFORE any
// field is parsed.
func fetchVerifiedManifest() (*manifest, error) {
	body, err := httpGetBytes(manifestURL)
	if err != nil {
		return nil, err
	}
	sigB64, err := httpGetBytes(manifestURL + ".sig")
	if err != nil {
		return nil, err
	}
	return parseVerifiedManifest(updatePublicKeyB64, body, sigB64)
}

// parseVerifiedManifest verifies sigB64 (base64-std) over the exact body bytes
// with pubB64, then unmarshals. FAIL-CLOSED: a bad key, bad signature encoding,
// or a failed verify returns an error (never a partial manifest).
func parseVerifiedManifest(pubB64 string, body, sigB64 []byte) (*manifest, error) {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil {
		return nil, err
	}
	if !verifyDetached(pubB64, body, sig) {
		return nil, errUnverifiedManifest
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// verifyDetached checks an ed25519 signature over msg using a base64-std public
// key. FAIL-CLOSED on every bad-input path: an unparseable/placeholder key, a
// wrong-length key, or an empty signature all return false.
func verifyDetached(pubB64 string, msg, sig []byte) bool {
	pub, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	if len(sig) == 0 {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), msg, sig)
}

// errUpdateVerify is the single fail-closed error for any update-binary
// verification failure. We deliberately do NOT distinguish the failure modes to
// the caller: any of them means "do not apply".
var errUpdateVerify = errors.New("beam: update binary verification failed")

// verifyUpdateBinary checks a downloaded update binary against the signed
// manifest fields BEFORE selfupdate.Apply is ever called. It recomputes the
// SHA-256 (hex) and compares it to wantSha256Hex, and verifies the detached
// Ed25519 sigB64 over the exact bytes with pubB64. Both must pass. FAIL-CLOSED:
// an empty field, a hash mismatch, an unparseable/short signature, or a
// placeholder/unparseable pubkey all return errUpdateVerify. This is the trust
// decision; the selfupdate library is not part of it.
func verifyUpdateBinary(pubB64 string, data []byte, wantSha256Hex, sigB64 string) error {
	if len(data) == 0 || wantSha256Hex == "" || sigB64 == "" {
		return errUpdateVerify
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(wantSha256Hex)) {
		return errUpdateVerify
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return errUpdateVerify
	}
	if !verifyDetached(pubB64, data, sig) {
		return errUpdateVerify
	}
	return nil
}

func httpGetBytes(u string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, http.ErrNotSupported
	}
	return io.ReadAll(resp.Body)
}

// semverNewer reports whether latest is a strictly newer release than current by
// ordered MAJOR.MINOR.PATCH. Rule (simplest correct): only the numeric core is
// compared; an optional leading "v" is stripped and any "-prerelease"/"+build"
// suffix is dropped before comparing. Anything that does not parse to three
// numeric parts is treated as "not newer" (no nag on garbage). A pre-release does
// not by itself trigger a nag - a higher core still wins on its core.
func semverNewer(latest, current string) bool {
	lv, ok1 := parseSemver(latest)
	cv, ok2 := parseSemver(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if lv[i] != cv[i] {
			return lv[i] > cv[i]
		}
	}
	return false
}

func parseSemver(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// belowMinVersion is the app-side twin of Core's beamClientBelowMin: it decides
// the MANDATORY tier. Empty/invalid min -> false (gating off). An unparseable
// current (incl. "dev") while a valid min is set -> true (fail-closed, matching
// the server gate so the app blocks itself exactly when Core would). Otherwise
// ordered MAJOR.MINOR.PATCH: current < min -> true. Reuses the Phase-1
// parseSemver rule; does not duplicate it.
func belowMinVersion(current, min string) bool {
	mv, ok := parseSemver(min)
	if !ok {
		return false
	}
	cv, ok := parseSemver(current)
	if !ok {
		return true
	}
	for i := 0; i < 3; i++ {
		if cv[i] != mv[i] {
			return cv[i] < mv[i]
		}
	}
	return false
}

// OpenUpdateDownload opens the update manifest's platform-specific download
// URL in the user's system browser (so it doesn't navigate the app's
// webview). Deliberately no-arg (BC3): OpenInBrowser used to take an
// arbitrary caller-supplied URL, but it is a Wails-bound method reachable as
// window.go.main.App.OpenInBrowser(...) from ANY JS in the webview - the
// Wails webview reverse-proxies the remote Panel onto the wails:// origin,
// so a compromised/MITM'd Panel (or a user-set malicious Panel URL) could
// call it with a file://, UNC, or other dangerous scheme, triggering a
// native OS shell-open. The no-arg design removes that JS-supplied-argument
// vector, but the manifest's DownloadURL is still Go-origin input passed
// straight to BrowserOpenURL, bypassing the Task 1 dispatcher's scheme
// check. The allowlist below additionally constrains even a poisoned or
// MITM'd manifest's DownloadURL to http/https, so it cannot reach a
// file:// shell-open either.
func (a *App) OpenUpdateDownload(token string) {
	if !a.checkShellToken(token) {
		return // broker isolation: only the first-party shell may open the browser
	}
	info := a.GetUpdateInfo()
	if info.DownloadURL == "" || a.ctx == nil {
		return
	}
	if u, err := url.Parse(info.DownloadURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return
	}
	wailsruntime.BrowserOpenURL(a.ctx, info.DownloadURL)
}
