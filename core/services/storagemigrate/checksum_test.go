package storagemigrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestChecksum_KnownVectors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		n    int64
	}{
		{"empty", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", 0},
		{"abc", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", 3},
		{"hello world", "hello world", "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9", 11},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sum, n, err := Checksum(strings.NewReader(c.in))
			if err != nil {
				t.Fatalf("Checksum err = %v, want nil", err)
			}
			if sum != c.want {
				t.Errorf("Checksum = %q, want %q", sum, c.want)
			}
			if n != c.n {
				t.Errorf("byte count = %d, want %d", n, c.n)
			}
		})
	}
}

func TestChecksum_LargerThanOneBuffer(t *testing.T) {
	// Deliberately exceeds checksumBufSize so the multi-read path is covered.
	body := bytes.Repeat([]byte("dylaris"), (checksumBufSize/7)+1000)
	want := sha256.Sum256(body)
	sum, n, err := Checksum(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Checksum err = %v", err)
	}
	if sum != hex.EncodeToString(want[:]) {
		t.Errorf("Checksum = %q, want %q", sum, hex.EncodeToString(want[:]))
	}
	if n != int64(len(body)) {
		t.Errorf("byte count = %d, want %d", n, len(body))
	}
}

func TestChecksum_SameSizeDifferentContentDiffers(t *testing.T) {
	// The load-bearing property: size alone can never stand in for content.
	a, _, err := Checksum(strings.NewReader("AAAAAAAA"))
	if err != nil {
		t.Fatalf("Checksum a: %v", err)
	}
	b, _, err := Checksum(strings.NewReader("AAAAAAAB"))
	if err != nil {
		t.Fatalf("Checksum b: %v", err)
	}
	if a == b {
		t.Fatal("same-size different-content produced the same checksum")
	}
}

func TestChecksum_PropagatesReadError(t *testing.T) {
	boom := errors.New("read exploded")
	if _, _, err := Checksum(&errReader{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("Checksum err = %v, want it to wrap %v", err, boom)
	}
}

func TestChecksumInto_WritesEveryByteAndHashesIt(t *testing.T) {
	var sink bytes.Buffer
	body := "the quick brown fox"
	sum, n, err := ChecksumInto(&sink, strings.NewReader(body))
	if err != nil {
		t.Fatalf("ChecksumInto err = %v", err)
	}
	if sink.String() != body {
		t.Errorf("sink = %q, want %q", sink.String(), body)
	}
	if n != int64(len(body)) {
		t.Errorf("byte count = %d, want %d", n, len(body))
	}
	direct, _, _ := Checksum(strings.NewReader(body))
	if sum != direct {
		t.Errorf("ChecksumInto sum = %q, want %q (same as Checksum)", sum, direct)
	}
}

func TestChecksumInto_PropagatesWriteError(t *testing.T) {
	boom := errors.New("disk full")
	if _, _, err := ChecksumInto(&errWriter{err: boom}, strings.NewReader("payload")); !errors.Is(err, boom) {
		t.Fatalf("ChecksumInto err = %v, want it to wrap %v", err, boom)
	}
}

func TestChecksumAlgoIsSHA256(t *testing.T) {
	if ChecksumAlgo != "sha256" {
		t.Fatalf("ChecksumAlgo = %q, want \"sha256\" (persisted into storage_manifests.algo)", ChecksumAlgo)
	}
}

// errReader always fails. Local to this file; never redeclared.
type errReader struct{ err error }

func (e *errReader) Read([]byte) (int, error) { return 0, e.err }

// errWriter always fails. Local to this file; never redeclared.
type errWriter struct{ err error }

func (e *errWriter) Write([]byte) (int, error) { return 0, e.err }

var _ io.Reader = (*errReader)(nil)
var _ io.Writer = (*errWriter)(nil)
