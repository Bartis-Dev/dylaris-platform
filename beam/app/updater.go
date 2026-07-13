package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
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

// manifest is the app's minimal read view of latest.json. The producer also
// writes per-platform "sha256" and "sig" fields (for Phase 3's self-apply);
// encoding/json ignores those unknown fields here. Integrity is not weakened by
// ignoring them: the manifest signature is verified over the FULL raw bytes
// before any field is read.
type manifest struct {
	Version   string `json:"version"`
	Platforms map[string]struct {
		URL string `json:"url"`
	} `json:"platforms"`
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
func (a *App) OpenUpdateDownload() {
	info := a.GetUpdateInfo()
	if info.DownloadURL == "" || a.ctx == nil {
		return
	}
	if u, err := url.Parse(info.DownloadURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return
	}
	wailsruntime.BrowserOpenURL(a.ctx, info.DownloadURL)
}
