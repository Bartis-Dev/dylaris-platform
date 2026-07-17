package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
