package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignVerifyRoundtrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bins := []binInput{{Slug: "linux-amd64", Data: []byte("fake-binary-bytes")}}
	m := buildManifest("1.2.3", "", "https://example.test/dl", priv, bins)

	entry, ok := m.Platforms["linux-amd64"]
	if !ok {
		t.Fatal("missing linux-amd64 entry")
	}
	if entry.URL != "https://example.test/dl/DylarisBeam-linux-amd64" {
		t.Errorf("url = %q", entry.URL)
	}
	// sha256 correctness.
	want := sha256.Sum256(bins[0].Data)
	if entry.Sha256 != hex.EncodeToString(want[:]) {
		t.Errorf("sha256 mismatch")
	}
	// per-binary signature verifies.
	binSig, err := base64.StdEncoding.DecodeString(entry.Sig)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(pub, bins[0].Data, binSig) {
		t.Error("per-binary signature did not verify")
	}

	// manifest signature verifies over the canonical bytes.
	manifestBytes, err := canonicalManifestBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	manifestSig := ed25519.Sign(priv, manifestBytes)
	if !ed25519.Verify(pub, manifestBytes, manifestSig) {
		t.Error("manifest signature did not verify")
	}

	// tampered manifest byte is rejected.
	tampered := append([]byte(nil), manifestBytes...)
	tampered[0] ^= 0xFF
	if ed25519.Verify(pub, tampered, manifestSig) {
		t.Error("tampered manifest verified - must fail")
	}

	// canonical bytes parse back to the expected shape.
	var got manifest
	if err := json.Unmarshal(manifestBytes, &got); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if got.Version != "1.2.3" || got.Platforms["linux-amd64"].URL != entry.URL {
		t.Errorf("re-parsed shape mismatch: %+v", got)
	}
}

func TestNoTrailingNewline(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bins := []binInput{{Slug: "linux-amd64", Data: []byte("fake-binary-bytes")}}
	m := buildManifest("1.2.3", "", "https://example.test/dl", priv, bins)

	manifestBytes, err := canonicalManifestBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasSuffix(manifestBytes, []byte("\n")) {
		t.Error("canonicalManifestBytes must not end in a trailing newline")
	}

	sig := signDetached(priv, manifestBytes)
	if strings.Contains(sig, "\n") {
		t.Error("signDetached output must not contain a newline")
	}
}

func TestCanonicalBytesDeterministic(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	m := buildManifest("2.0.0", "", "https://x.test", priv, []binInput{{Slug: "linux-amd64", Data: []byte("a")}})
	b1, _ := canonicalManifestBytes(m)
	b2, _ := canonicalManifestBytes(m)
	if string(b1) != string(b2) {
		t.Error("canonical bytes are not stable")
	}
}

// TestBuildManifestMinVersion pins the force-update floor plumbing: a non-empty
// min-version is embedded and travels under the manifest signature, while an
// empty one is OMITTED so a floor-less release stays byte-identical to the
// legacy manifest format (no "minVersion" key at all).
func TestBuildManifestMinVersion(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bins := []binInput{{Slug: "linux-amd64", Data: []byte("bin")}}

	// With a floor: the field is present and its value survives a sign/verify
	// round-trip over the exact manifest bytes.
	withMin := buildManifest("1.4.0", "1.3.0", "https://x.test", priv, bins)
	if withMin.MinVersion != "1.3.0" {
		t.Errorf("MinVersion = %q, want 1.3.0", withMin.MinVersion)
	}
	mb, err := canonicalManifestBytes(withMin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mb), `"minVersion": "1.3.0"`) {
		t.Errorf("manifest bytes missing minVersion field: %s", mb)
	}
	sig, err := base64.StdEncoding.DecodeString(signDetached(priv, mb))
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(pub, mb, sig) {
		t.Error("signed manifest with minVersion did not verify")
	}
	var reparsed manifest
	if err := json.Unmarshal(mb, &reparsed); err != nil {
		t.Fatal(err)
	}
	if reparsed.MinVersion != "1.3.0" {
		t.Errorf("re-parsed MinVersion = %q, want 1.3.0", reparsed.MinVersion)
	}

	// Without a floor: omitempty drops the key entirely.
	noMin := buildManifest("1.4.0", "", "https://x.test", priv, bins)
	nb, err := canonicalManifestBytes(noMin)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(nb), "minVersion") {
		t.Errorf("empty min-version must omit the field, got: %s", nb)
	}
}

// TestParsableMinVersionMatchesTheApp pins the producer's rule to the consumer's.
//
// The app's belowMinVersion returns false for a floor it cannot parse, so an
// unparseable value is not an error anywhere: the release succeeds, the manifest
// verifies, and the gate simply never fires. These cases are the ones that would
// have shipped that way.
func TestParsableMinVersionMatchesTheApp(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"1.2.3", true},
		{"v1.2.3", true},    // the app strips a leading v
		{"1.2.3-rc1", true}, // and everything from the first - or +
		{"1.2.3+build", true},
		{"1.2", false}, // two parts: the app gives up, silently
		{"1.2.3.4", false},
		{"latest", false},
		{"1.x.0", false},
		{"", false},
	} {
		if got := parsableMinVersion(c.in); got != c.want {
			t.Errorf("parsableMinVersion(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestCommittedMinVersionIsUsable reads the real beam/app/MIN_VERSION, the file
// the release workflow passes to -min-version.
//
// It is the only guard on that value: a typo there cannot fail a build, cannot
// fail a signature, and cannot be seen in the published manifest without knowing
// what to look for. It would just be a force-update that quietly does nothing on
// the one day it was needed.
func TestCommittedMinVersionIsUsable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "MIN_VERSION"))
	if err != nil {
		t.Fatalf("MIN_VERSION is missing - the release workflow reads it: %v", err)
	}
	var value string
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "MIN_VERSION="); ok {
			value = strings.TrimSpace(v)
		}
	}
	if value == "" {
		return // no floor is the normal case
	}
	if !parsableMinVersion(value) {
		t.Errorf("MIN_VERSION=%q would be silently ignored by the app; use x.y.z", value)
	}
}
