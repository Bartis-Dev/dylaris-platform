package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

// The host key IS the SFTP server's identity. If it changes, every client that
// has connected before refuses with a host-key-changed warning - which is what
// used to happen on every container recreate, because the key lived on the
// container layer.

// writeTestKey generates a small RSA key and writes it PEM-encoded. 1024 bits
// because these tests only care about identity, not strength, and 4096 would
// make the suite noticeably slow.
func writeTestKey(t *testing.T, path string) ssh.Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func fingerprint(s ssh.Signer) string {
	return ssh.FingerprintSHA256(s.PublicKey())
}

// The bug this whole change was about: the key has to survive.
func TestLoadOrGenHostKey_ReusesAnExistingKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "sftp_host_key")
	want := writeTestKey(t, keyPath)

	got, err := loadOrGenHostKeyAt(keyPath, filepath.Join(dir, "legacy"))
	if err != nil {
		t.Fatalf("loadOrGenHostKeyAt: %v", err)
	}
	if fingerprint(got) != fingerprint(want) {
		t.Error("returned a different key than the one on disk")
	}
}

// Upgrading a node must not change its fingerprint, so a key still in the old
// location is adopted rather than replaced.
func TestLoadOrGenHostKey_AdoptsTheLegacyKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "new", "sftp_host_key")
	legacyPath := filepath.Join(dir, "old", "sftp_host_key")
	want := writeTestKey(t, legacyPath)

	got, err := loadOrGenHostKeyAt(keyPath, legacyPath)
	if err != nil {
		t.Fatalf("loadOrGenHostKeyAt: %v", err)
	}
	if fingerprint(got) != fingerprint(want) {
		t.Fatal("did not adopt the legacy key - every client would see a changed fingerprint")
	}
	// And it must be copied across, or the adoption would repeat every boot and
	// break the moment the legacy directory goes away.
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("legacy key was not persisted to the new location: %v", err)
	}
}

// The persistent location wins over the legacy one, so a node that has already
// migrated does not fall back to a stale copy.
func TestLoadOrGenHostKey_PrefersThePersistentLocation(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "new", "sftp_host_key")
	legacyPath := filepath.Join(dir, "old", "sftp_host_key")
	want := writeTestKey(t, keyPath)
	legacy := writeTestKey(t, legacyPath)

	got, err := loadOrGenHostKeyAt(keyPath, legacyPath)
	if err != nil {
		t.Fatalf("loadOrGenHostKeyAt: %v", err)
	}
	if fingerprint(got) == fingerprint(legacy) {
		t.Fatal("fell back to the legacy key despite a current one being present")
	}
	if fingerprint(got) != fingerprint(want) {
		t.Error("did not return the key at the persistent location")
	}
}

// A corrupt file must not wedge the server: generate a fresh key rather than
// failing to start. The fingerprint changes, but a node that cannot start
// serves nobody.
func TestLoadOrGenHostKey_ReplacesACorruptKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "sftp_host_key")
	if err := os.WriteFile(keyPath, []byte("this is not a PEM key"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := loadOrGenHostKeyAt(keyPath, filepath.Join(dir, "legacy"))
	if err != nil {
		t.Fatalf("loadOrGenHostKeyAt: %v", err)
	}
	if got == nil {
		t.Fatal("no signer returned")
	}
	// The replacement has to be written, or the next boot regenerates again and
	// the fingerprint changes on every restart.
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if parseHostKey(data) == nil {
		t.Error("the corrupt file was not replaced with a usable key")
	}
}

// Two runs in a row must agree - that is what "survives a recreate" means.
func TestLoadOrGenHostKey_IsStableAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "sftp_host_key")
	legacyPath := filepath.Join(dir, "legacy")

	first, err := loadOrGenHostKeyAt(keyPath, legacyPath)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := loadOrGenHostKeyAt(keyPath, legacyPath)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if fingerprint(first) != fingerprint(second) {
		t.Error("the fingerprint changed between runs")
	}
}

func TestParseHostKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "k")
	writeTestKey(t, keyPath)
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if parseHostKey(data) == nil {
		t.Error("a valid PEM key did not parse")
	}

	for _, bad := range [][]byte{
		nil,
		[]byte(""),
		[]byte("not pem at all"),
		[]byte("-----BEGIN RSA PRIVATE KEY-----\nbm90IGEga2V5\n-----END RSA PRIVATE KEY-----\n"),
	} {
		if parseHostKey(bad) != nil {
			t.Errorf("parseHostKey(%q) returned a signer", string(bad))
		}
	}
}

// The path follows the node identity onto a directory that survives a recreate;
// the legacy constant is only the fallback when no storage path is configured.
func TestSFTPHostKeyPath(t *testing.T) {
	original := nodeSecretDir
	t.Cleanup(func() { nodeSecretDir = original })

	nodeSecretDir = filepath.Join("tmp", "storage")
	if got, want := sftpHostKeyPath(), filepath.Join("tmp", "storage", "sftp_host_key"); got != want {
		t.Errorf("sftpHostKeyPath() = %q, want %q", got, want)
	}

	nodeSecretDir = ""
	if got := sftpHostKeyPath(); got != filepath.FromSlash(legacySFTPHostKeyPath) {
		t.Errorf("sftpHostKeyPath() with no storage dir = %q, want the legacy path", got)
	}
}
