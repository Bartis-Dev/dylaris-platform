package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestRunSignMultiTargetWindows is a regression guard: it drives the real
// runSign entry point over a multi-target set that INCLUDES windows-amd64 and
// asserts the emitted latest.json + detached sig files. The sign tool already
// handles arbitrary <os-arch> slugs (buildManifest keys the platform map on the
// slug and derives the URL as "<base>/DylarisBeam-<slug>"), so this adds NO
// production code; it pins that a windows-amd64 target flows through unchanged,
// which WS6's CI relies on.
func TestRunSignMultiTargetWindows(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// runSign reads the private seed from BEAM_UPDATE_PRIVKEY (base64 std seed).
	t.Setenv("BEAM_UPDATE_PRIVKEY", base64.StdEncoding.EncodeToString(priv.Seed()))

	out := t.TempDir()
	linData := []byte("fake-linux-binary")
	winData := []byte("fake-windows-binary")
	linPath := filepath.Join(out, "DylarisBeam-linux-amd64")
	winPath := filepath.Join(out, "DylarisBeam-windows-amd64")
	if err := os.WriteFile(linPath, linData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(winPath, winData, 0o644); err != nil {
		t.Fatal(err)
	}

	const baseURL = "https://example.test/dl"
	err = runSign([]string{
		"-version", "1.5.0",
		"-base-url", baseURL,
		"-out", out,
		"linux-amd64=" + linPath,
		"windows-amd64=" + winPath,
	})
	if err != nil {
		t.Fatalf("runSign errored: %v", err)
	}

	// latest.json parses and carries the version.
	manifestBytes, err := os.ReadFile(filepath.Join(out, "latest.json"))
	if err != nil {
		t.Fatalf("read latest.json: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		t.Fatalf("parse latest.json: %v", err)
	}
	if m.Version != "1.5.0" {
		t.Errorf("manifest version = %q, want 1.5.0", m.Version)
	}

	// Per-slug expectations, table-driven over the two targets.
	cases := []struct {
		slug string
		data []byte
	}{
		{"linux-amd64", linData},
		{"windows-amd64", winData},
	}
	for _, c := range cases {
		t.Run(c.slug, func(t *testing.T) {
			entry, ok := m.Platforms[c.slug]
			if !ok {
				t.Fatalf("latest.json missing %q entry", c.slug)
			}
			wantURL := baseURL + "/DylarisBeam-" + c.slug
			if entry.URL != wantURL {
				t.Errorf("url = %q, want %q", entry.URL, wantURL)
			}
			sum := sha256.Sum256(c.data)
			if entry.Sha256 != hex.EncodeToString(sum[:]) {
				t.Errorf("sha256 = %q, want %q", entry.Sha256, hex.EncodeToString(sum[:]))
			}
			sig, err := base64.StdEncoding.DecodeString(entry.Sig)
			if err != nil {
				t.Fatalf("decode entry sig: %v", err)
			}
			if !ed25519.Verify(pub, c.data, sig) {
				t.Errorf("manifest sig for %q did not verify", c.slug)
			}
			// Detached per-binary sig file uses the DylarisBeam-<slug>.sig scheme.
			sigPath := filepath.Join(out, "DylarisBeam-"+c.slug+".sig")
			if _, err := os.Stat(sigPath); err != nil {
				t.Errorf("missing detached sig file %s: %v", sigPath, err)
			}
		})
	}

	// The manifest itself is signed detached over its exact bytes.
	manifestSig, err := os.ReadFile(filepath.Join(out, "latest.json.sig"))
	if err != nil {
		t.Fatalf("read latest.json.sig: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(string(manifestSig))
	if err != nil {
		t.Fatalf("decode manifest sig: %v", err)
	}
	if !ed25519.Verify(pub, manifestBytes, sig) {
		t.Error("latest.json.sig did not verify over latest.json bytes")
	}
}
