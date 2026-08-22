// Command beam-release is the in-repo Ed25519 signing tool for beam releases.
// It holds NO key: keygen emits a fresh keypair for the owner to store, and
// sign reads the private seed from the BEAM_UPDATE_PRIVKEY env var at CI time.
// stdlib only.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: beam-release <keygen|sign> [args]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "keygen":
		if err := runKeygen(); err != nil {
			fmt.Fprintln(os.Stderr, "keygen:", err)
			os.Exit(1)
		}
	case "sign":
		if err := runSign(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "sign:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", os.Args[1])
		os.Exit(2)
	}
}

// runKeygen prints a fresh keypair. The private seed is secret (-> CI secret
// BEAM_UPDATE_PRIVKEY); the public key is committed to beam/app/update_pubkey.go.
func runKeygen() error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	fmt.Println("BEAM_UPDATE_PRIVKEY (base64 seed, SECRET - store in CI, never commit):")
	fmt.Println("  " + base64.StdEncoding.EncodeToString(priv.Seed()))
	fmt.Println("public key (base64 - paste into beam/app/update_pubkey.go):")
	fmt.Println("  " + base64.StdEncoding.EncodeToString(pub))
	return nil
}

// runSign signs one or more built binaries and emits latest.json + detached sigs.
// Positional args are "<os-arch>=<binaryPath>", e.g. "linux-amd64=dist/DylarisBeam-linux-amd64".
func runSign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	version := fs.String("version", "", "release version, e.g. 1.2.3")
	baseURL := fs.String("base-url", "", "release asset base URL")
	outDir := fs.String("out", ".", "output directory for latest.json and .sig files")
	// minVersion bakes the force-update floor into the SIGNED manifest so Core's
	// "auto" min-version mode can follow it without a separate unsigned fetch. Empty
	// omits the field entirely (manifest stays byte-identical to a pre-min release).
	minVersion := fs.String("min-version", "", "force-update floor to embed in the signed manifest, e.g. 1.2.0 (optional)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse args: %w", err)
	}
	if *version == "" || *baseURL == "" {
		return fmt.Errorf("-version and -base-url are required")
	}
	min := strings.TrimSpace(*minVersion)
	if min != "" && !parsableMinVersion(min) {
		return fmt.Errorf("-min-version %q is not x.y.z; the app would silently ignore it and the gate would never fire", min)
	}
	seedB64 := os.Getenv("BEAM_UPDATE_PRIVKEY")
	if seedB64 == "" {
		return fmt.Errorf("BEAM_UPDATE_PRIVKEY not set")
	}
	seed, err := base64.StdEncoding.DecodeString(seedB64)
	if err != nil {
		return fmt.Errorf("decode BEAM_UPDATE_PRIVKEY: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return fmt.Errorf("BEAM_UPDATE_PRIVKEY: want %d-byte seed, got %d", ed25519.SeedSize, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)

	var bins []binInput
	for _, a := range fs.Args() {
		slug, path, ok := strings.Cut(a, "=")
		if !ok {
			return fmt.Errorf("bad binary arg %q, want <os-arch>=<path>", a)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read binary %s (%s): %w", slug, path, err)
		}
		bins = append(bins, binInput{Slug: slug, Data: data})
		// Detached per-binary signature, named after the asset it signs.
		sigPath := *outDir + "/" + assetName(slug) + ".sig"
		if err := os.WriteFile(sigPath, []byte(signDetached(priv, data)), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", sigPath, err)
		}
	}
	if len(bins) == 0 {
		return fmt.Errorf("no binaries given")
	}

	m := buildManifest(*version, min, *baseURL, priv, bins)
	manifestBytes, err := canonicalManifestBytes(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outDir+"/latest.json", manifestBytes, 0o644); err != nil {
		return fmt.Errorf("write latest.json: %w", err)
	}
	// Detached signature over the EXACT latest.json bytes.
	sig := signDetached(priv, manifestBytes)
	if err := os.WriteFile(*outDir+"/latest.json.sig", []byte(sig), 0o644); err != nil {
		return fmt.Errorf("write latest.json.sig: %w", err)
	}
	return nil
}

// parsableMinVersion reports whether the app's own parser would accept this
// floor. It mirrors parseSemver in beam/app/updater.go, which cannot be imported
// (both are package main in different modules' commands), so the rule is
// duplicated on purpose - the same way update_pubkey.go is duplicated into Core.
//
// This check exists because the failure it prevents is SILENT. belowMinVersion
// returns false for a floor it cannot parse, so a typo does not fail the
// release, produce a warning, or block anyone: it ships a manifest whose gate
// can never fire, and nobody finds out until the day the floor was supposed to
// matter. Refusing at sign time turns that into a failed release instead.
func parsableMinVersion(s string) bool {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return false
		}
	}
	return true
}

// assetName is the release-asset filename for a platform slug. Windows carries a
// .exe extension so GitHub serves it as an executable and users can double-click
// it; every other OS is extensionless. The updater fetches exactly this name via
// the manifest URL, so the signed + published Windows binary is a SINGLE file -
// no byte-identical extensionless duplicate.
func assetName(slug string) string {
	name := "DylarisBeam-" + slug
	if strings.HasPrefix(slug, "windows-") {
		name += ".exe"
	}
	return name
}

type platformEntry struct {
	URL    string `json:"url"`
	Sha256 string `json:"sha256"`
	Sig    string `json:"sig"`
}

type manifest struct {
	Version string `json:"version"`
	// MinVersion is the optional force-update floor. Omitted when empty so a
	// release without a floor produces a manifest byte-identical to the legacy
	// (pre-min-version) format. Core's auto min-version mode reads it AFTER
	// verifying the manifest signature.
	MinVersion string                   `json:"minVersion,omitempty"`
	Platforms  map[string]platformEntry `json:"platforms"`
}

type binInput struct {
	Slug string
	Data []byte
}

func buildManifest(version, minVersion, baseURL string, priv ed25519.PrivateKey, bins []binInput) manifest {
	m := manifest{Version: version, MinVersion: minVersion, Platforms: map[string]platformEntry{}}
	for _, b := range bins {
		m.Platforms[b.Slug] = platformEntry{
			URL:    baseURL + "/" + assetName(b.Slug),
			Sha256: sha256Hex(b.Data),
			Sig:    signDetached(priv, b.Data),
		}
	}
	return m
}

func canonicalManifestBytes(m manifest) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

func signDetached(priv ed25519.PrivateKey, data []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
