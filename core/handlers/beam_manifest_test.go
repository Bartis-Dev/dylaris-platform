package handlers

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dylaris-core/store"
)

// signManifestForTest marshals a manifest body and returns (bodyBytes, sigB64)
// signed with priv, mirroring what the beam-release sign tool writes.
func signManifestForTest(t *testing.T, priv ed25519.PrivateKey, version, minVersion string) ([]byte, string) {
	t.Helper()
	m := map[string]any{"version": version, "platforms": map[string]any{}}
	if minVersion != "" {
		m["minVersion"] = minVersion
	}
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body))
	return body, sig
}

func TestVerifyBeamManifest(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	body, sigB64 := signManifestForTest(t, priv, "1.4.0", "1.3.0")

	// Happy path: verifies and extracts both fields.
	m, err := verifyBeamManifest(pubB64, body, []byte(sigB64))
	if err != nil {
		t.Fatalf("verify errored on a good manifest: %v", err)
	}
	if m.Version != "1.4.0" || m.MinVersion != "1.3.0" {
		t.Errorf("parsed manifest = %+v, want version 1.4.0 minVersion 1.3.0", m)
	}

	// Tampered body: signature no longer matches.
	tampered := append([]byte(nil), body...)
	tampered[0] ^= 0xFF
	if _, err := verifyBeamManifest(pubB64, tampered, []byte(sigB64)); err == nil {
		t.Error("verify accepted a tampered body")
	}

	// Wrong key: a different signer must not verify.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := verifyBeamManifest(base64.StdEncoding.EncodeToString(otherPub), body, []byte(sigB64)); err == nil {
		t.Error("verify accepted a signature from the wrong key")
	}

	// Malformed inputs all fail closed.
	if _, err := verifyBeamManifest("not-base64!!", body, []byte(sigB64)); err == nil {
		t.Error("verify accepted an unparseable public key")
	}
	if _, err := verifyBeamManifest(base64.StdEncoding.EncodeToString([]byte("too-short")), body, []byte(sigB64)); err == nil {
		t.Error("verify accepted a wrong-length public key")
	}
	if _, err := verifyBeamManifest(pubB64, body, []byte("")); err == nil {
		t.Error("verify accepted an empty signature")
	}
	if _, err := verifyBeamManifest(pubB64, body, []byte("!!bad-b64")); err == nil {
		t.Error("verify accepted an unparseable signature")
	}
}

func TestFetchVerifiedBeamMinVersion(t *testing.T) {
	// httptest binds 127.0.0.1, which the beam fetch guard refuses on purpose.
	allowLoopbackBeamDial(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	body, sigB64 := signManifestForTest(t, priv, "2.0.0", "1.9.0")

	// A server that serves latest.json + latest.json.sig from a small mux.
	mkServer := func(manifestBody []byte, sig string, status int) *httptest.Server {
		mux := http.NewServeMux()
		mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) {
			if status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
			w.Write(manifestBody)
		})
		mux.HandleFunc("/latest.json.sig", func(w http.ResponseWriter, r *http.Request) {
			if status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
			w.Write([]byte(sig))
		})
		return httptest.NewServer(mux)
	}

	// Happy path: returns the embedded minVersion.
	srv := mkServer(body, sigB64, http.StatusOK)
	defer srv.Close()
	got, err := fetchVerifiedBeamMinVersion(context.Background(), srv.URL+"/latest.json", pubB64)
	if err != nil {
		t.Fatalf("fetch errored: %v", err)
	}
	if got != "1.9.0" {
		t.Errorf("minVersion = %q, want 1.9.0", got)
	}

	// 404: fetch error surfaces.
	bad := mkServer(body, sigB64, http.StatusNotFound)
	defer bad.Close()
	if _, err := fetchVerifiedBeamMinVersion(context.Background(), bad.URL+"/latest.json", pubB64); err == nil {
		t.Error("fetch accepted a 404 manifest")
	}

	// Wrong key: signature does not verify.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := fetchVerifiedBeamMinVersion(context.Background(), srv.URL+"/latest.json", base64.StdEncoding.EncodeToString(otherPub)); err == nil {
		t.Error("fetch accepted a manifest signed by the wrong key")
	}
}

// beamManifestFakeStore embeds store.Store (nil) and overrides only GetSetting,
// which is all effectiveMinVersion touches. Any other call would panic.
type beamManifestFakeStore struct {
	store.Store
	settings map[string]string
}

func (f *beamManifestFakeStore) GetSetting(key string) (string, error) {
	return f.settings[key], nil
}

func TestEffectiveMinVersion(t *testing.T) {
	// The floor comes from the SIGNED manifest and nowhere else. A leftover
	// beam.min_version row from before the manual mode was removed must not
	// resurrect itself as a floor - that is the regression this asserts.
	unverifiable := func(t *testing.T) string {
		t.Helper()
		_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
		body, sig := signManifestForTest(t, otherPriv, "5.0.0", "4.0.0")
		mux := http.NewServeMux()
		mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
		mux.HandleFunc("/latest.json.sig", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sig)) })
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return srv.URL + "/latest.json"
	}

	// Signed by a key that is NOT the embedded beamUpdatePublicKeyB64:
	// verification fails, so the floor resolves to "" (gate off) rather than
	// trusting an unverifiable manifest - even though a stale manual floor is
	// still sitting in settings.
	autoStore := &beamManifestFakeStore{settings: map[string]string{
		"beam.min_version":      "1.2.3",
		"beam.min_version_mode": "manual",
		"beam.release_manifest": unverifiable(t),
	}}
	hAuto := &BeamHandler{state: &AppState{Store: autoStore}}
	if got := hAuto.effectiveMinVersion(context.Background()); got != "" {
		t.Errorf("unverifiable manifest floor = %q, want \"\" (fail-open, retired settings ignored)", got)
	}

	// No manifest reachable at all: same answer, gate off.
	deadStore := &beamManifestFakeStore{settings: map[string]string{
		"beam.min_version":      "2.0.0",
		"beam.release_manifest": "http://127.0.0.1:1/never",
	}}
	hDead := &BeamHandler{state: &AppState{Store: deadStore}}
	if got := hDead.effectiveMinVersion(context.Background()); got != "" {
		t.Errorf("unreachable manifest floor = %q, want \"\" (fail-open)", got)
	}
}
