package handlers

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultBeamManifestURL is the stable-channel signed update manifest on GitHub
// Releases. "latest/download" always resolves to the newest NON-prerelease
// release's fixed-name assets. Shared by the download-URL resolver and the auto
// min-version follower; overridable per-deploy via the beam.release_manifest
// setting. Public once the repo is public (owner go-live step).
const defaultBeamManifestURL = "https://github.com/Bartis-Dev/dylaris-platform/releases/latest/download/latest.json"

// beamUpdatePublicKeyB64 is the base64 (std) Ed25519 PUBLIC key that signs the
// beam update manifest (latest.json). It MUST stay byte-identical to the app's
// copy in platform/beam/app/update_pubkey.go: Core follows the signed manifest's
// minVersion in "auto" mode and the app verifies its own self-update, so both
// ends must root trust in the same key. Public keys are safe to commit and to
// duplicate across the two modules (beam/app is a separate go.work member and
// cannot be imported here).
//
// This is the REAL signing key; the matching private seed lives only in the CI
// secret BEAM_UPDATE_PRIVKEY (generated once via `go run ./cmd/beam-release
// keygen` from platform/beam/app). If this value is emptied or drifts from the
// app's copy, no release signature matches, so auto min-version resolves to ""
// (gate off). Fail-OPEN on the floor is deliberate: an unverifiable manifest must
// never be trusted to raise the floor and lock every client out.
const beamUpdatePublicKeyB64 = "5WS1g2Ushib55CKq7duxHJlcJKIwD7L3wYdA6coTiA4="

// beamMinVersionMode values for the beam.min_version_mode setting.
const (
	beamMinVersionModeManual = "manual" // admin-set beam.min_version wins (default)
	beamMinVersionModeAuto   = "auto"   // follow the signed manifest's minVersion
)

// beamMinAutoTTL bounds how often Core re-fetches the signed manifest for the
// auto min-version floor. GetBeamTicket runs on every connect, so an uncached
// fetch per request would add a GitHub round-trip to the hot connect path.
const beamMinAutoTTL = 5 * time.Minute

// errBeamManifestUnverified is the single fail-closed error for any manifest
// signature failure. Callers translate it to "no auto floor" ("").
var errBeamManifestUnverified = errors.New("beam: manifest signature verification failed")

// beamManifest is Core's read view of the signed latest.json. Only the fields
// Core needs are modeled; the platforms map and per-binary sigs are ignored here
// (the app consumes those). Read ONLY after the signature verifies.
type beamManifest struct {
	Version    string `json:"version"`
	MinVersion string `json:"minVersion"`
}

// verifyBeamManifest verifies sigB64 (base64-std, over the exact body bytes)
// against pubB64, then unmarshals. FAIL-CLOSED on every bad-input path: an
// unparseable/placeholder/wrong-length key, a bad signature encoding, an empty
// signature, or a failed verify all return errBeamManifestUnverified and never a
// partial manifest. Mirrors the app's parseVerifiedManifest so both ends agree.
func verifyBeamManifest(pubB64 string, body, sigB64 []byte) (*beamManifest, error) {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errBeamManifestUnverified
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || len(sig) == 0 {
		return nil, errBeamManifestUnverified
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), body, sig) {
		return nil, errBeamManifestUnverified
	}
	var m beamManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// fetchVerifiedBeamMinVersion downloads latest.json + latest.json.sig from
// manifestURL, verifies the signature with pubB64, and returns the manifest's
// embedded minVersion (may be ""). Any fetch or verify error is returned so the
// caller treats it as "no auto floor".
func fetchVerifiedBeamMinVersion(ctx context.Context, manifestURL, pubB64 string) (string, error) {
	body, err := httpGetBeamManifestBytes(ctx, manifestURL)
	if err != nil {
		return "", err
	}
	sig, err := httpGetBeamManifestBytes(ctx, manifestURL+".sig")
	if err != nil {
		return "", err
	}
	m, err := verifyBeamManifest(pubB64, body, sig)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(m.MinVersion), nil
}

// httpGetBeamManifestBytes GETs a small manifest asset with a tight timeout and a
// 1 MiB read cap (a manifest is a few hundred bytes; the cap guards against a
// hostile/oversized response on the connect hot path).
func httpGetBeamManifestBytes(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest fetch %s: HTTP %d", u, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// resolveBeamMinVersion is the pure force-update-floor decision. In auto mode the
// floor is the signed manifest's minVersion (autoMin, already verified upstream;
// "" when no manifest was verifiable, which disables the gate rather than locking
// everyone out). In any other mode (manual, the default) the admin-set manualMin
// wins verbatim. Pure and side-effect free for table testing.
func resolveBeamMinVersion(mode, manualMin, autoMin string) string {
	if strings.TrimSpace(mode) == beamMinVersionModeAuto {
		return autoMin
	}
	return manualMin
}

// effectiveMinVersion returns the force-update floor Core advertises (GetBeamConfig)
// and enforces (GetBeamTicket), honoring the beam.min_version_mode setting. Manual
// mode (default) returns the admin-set beam.min_version with no network I/O. Auto
// mode returns the signed manifest's minVersion, fetched+verified at most once per
// beamMinAutoTTL and cached; a fetch/verify failure yields "" (gate off) so a
// transient GitHub error can never lock every client out.
func (h *BeamHandler) effectiveMinVersion(ctx context.Context) string {
	getSetting := func(key string) string {
		v, _ := h.state.Store.GetSetting(key)
		return v
	}
	mode := strings.TrimSpace(getSetting("beam.min_version_mode"))
	manualMin := strings.TrimSpace(getSetting("beam.min_version"))
	if mode != beamMinVersionModeAuto {
		return manualMin // manual (default): no fetch
	}
	return resolveBeamMinVersion(mode, manualMin, h.cachedAutoMinVersion(ctx, getSetting))
}

// cachedAutoMinVersion returns the verified-manifest floor, refreshing at most
// once per beamMinAutoTTL. The outcome is cached either way (min="" on error) so
// a failing fetch does not re-hit GitHub on every connect; the floor simply stays
// off until the next TTL window resolves a verifiable manifest. Guarded so
// concurrent connects don't stampede the fetch.
func (h *BeamHandler) cachedAutoMinVersion(ctx context.Context, getSetting func(string) string) string {
	h.minCacheMu.Lock()
	defer h.minCacheMu.Unlock()
	if !h.minCacheAt.IsZero() && time.Since(h.minCacheAt) < beamMinAutoTTL {
		return h.minCacheVal
	}
	url := strings.TrimSpace(getSetting("beam.release_manifest"))
	if url == "" {
		url = defaultBeamManifestURL
	}
	min, err := fetchVerifiedBeamMinVersion(ctx, url, beamUpdatePublicKeyB64)
	if err != nil {
		min = ""
	}
	h.minCacheVal = min
	h.minCacheAt = time.Now()
	return min
}
