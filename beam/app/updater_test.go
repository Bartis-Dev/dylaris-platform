package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"runtime"
	"testing"
)

func TestSemverNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.2.3", "1.2.2", true},
		{"1.10.0", "1.9.9", true},
		{"2.0.0", "1.9.9", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.2", "1.2.3", false},
		{"1.2.3-rc1", "1.2.3", false}, // equal core: pre-release does not nag
		{"1.3.0-rc1", "1.2.9", true},  // higher core wins despite pre-release
		{"v1.2.4", "1.2.3", true},     // leading v tolerated
		{"garbage", "1.2.3", false},   // malformed -> no nag
		{"1.2.3", "dev", false},       // "dev" is not semver -> no nag
		{"1.2", "1.1.9", false},       // wrong part count -> no nag
	}
	for _, c := range cases {
		if got := semverNewer(c.latest, c.current); got != c.want {
			t.Errorf("semverNewer(%q,%q)=%v want %v", c.latest, c.current, got, c.want)
		}
	}
}

// canonicalManifest mirrors Task 1's producer: json.MarshalIndent(m, "", "  ").
func canonicalManifest(t *testing.T) []byte {
	t.Helper()
	m := map[string]any{
		"version": "1.4.0",
		"platforms": map[string]any{
			"linux-amd64": map[string]any{
				"url":    "https://example.test/DylarisBeam-linux-amd64",
				"sha256": "00",
				"sig":    "AA==",
			},
		},
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseVerifiedManifest(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	body := canonicalManifest(t)
	goodSig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body)))

	// valid signature accepted, fields parsed.
	m, err := parseVerifiedManifest(pubB64, body, goodSig)
	if err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if m.Version != "1.4.0" {
		t.Errorf("version = %q", m.Version)
	}

	// tampered body rejected (fail-closed).
	bad := append([]byte(nil), body...)
	bad[0] ^= 0xFF
	if _, err := parseVerifiedManifest(pubB64, bad, goodSig); err == nil {
		t.Error("tampered manifest accepted - must fail closed")
	}

	// missing/empty signature rejected.
	if _, err := parseVerifiedManifest(pubB64, body, []byte("")); err == nil {
		t.Error("empty signature accepted - must fail closed")
	}

	// wrong key rejected.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := parseVerifiedManifest(base64.StdEncoding.EncodeToString(otherPub), body, goodSig); err == nil {
		t.Error("wrong key accepted - must fail closed")
	}

	// the embedded PLACEHOLDER key rejects everything (fail-closed).
	if verifyDetached(updatePublicKeyB64, body, ed25519.Sign(priv, body)) {
		t.Error("placeholder key verified - must fail closed")
	}
}

func TestBelowMinVersion(t *testing.T) {
	cases := []struct {
		name         string
		current, min string
		want         bool
	}{
		{"gating off empty min", "1.0.0", "", false},
		{"malformed min fails safe", "1.0.0", "nope", false},
		{"below blocked", "1.2.2", "1.2.3", true},
		{"equal allowed", "1.2.3", "1.2.3", false},
		{"above allowed", "1.3.0", "1.2.3", false},
		{"v-prefixed below", "v1.2.2", "1.2.3", true},
		{"dev fail-closed", "dev", "1.0.0", true},
		{"empty current fail-closed", "", "1.0.0", true},
	}
	for _, c := range cases {
		if got := belowMinVersion(c.current, c.min); got != c.want {
			t.Errorf("%s: belowMinVersion(%q,%q) = %v want %v", c.name, c.current, c.min, got, c.want)
		}
	}
}

func TestPlatformArtifactDecode(t *testing.T) {
	key := runtime.GOOS + "-" + runtime.GOARCH
	// A manifest whose platform map has an entry for THIS host, so the resolver
	// finds it regardless of where the test runs. Confirms sha256/sig now decode.
	body := []byte(`{"version":"1.4.0","platforms":{"` + key +
		`":{"url":"https://example.test/bin","sha256":"abcd","sig":"AA=="}}}`)
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	url, sha, sig, err := platformArtifact(&m)
	if err != nil {
		t.Fatalf("platformArtifact(%s) errored: %v", key, err)
	}
	if url != "https://example.test/bin" || sha != "abcd" || sig != "AA==" {
		t.Errorf("got url=%q sha=%q sig=%q", url, sha, sig)
	}
	// No entry for this platform -> a clear error, not a crash.
	if _, _, _, err := platformArtifact(&manifest{Platforms: nil}); err == nil {
		t.Error("missing-platform manifest accepted - want error")
	}
}
