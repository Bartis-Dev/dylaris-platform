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

func TestChecksum_LargeBody(t *testing.T) {
	// checksumBufSize is 1 MiB; span several buffers plus a remainder so a
	// chunked copy loop unambiguously needs more than one Read to finish.
	const bodySize = checksumBufSize*3 + 12345
	body := bytes.Repeat([]byte("dylaris-"), bodySize/8+1)[:bodySize]
	wantSum := sha256.Sum256(body)
	want := hex.EncodeToString(wantSum[:])

	cases := []struct {
		name string
		// newReader returns a fresh reader over body each call so the two
		// subtests do not share read position.
		newReader  func() io.Reader
		checkReads bool
	}{
		{
			name:      "bytes.Reader implements io.WriterTo so io.CopyBuffer takes its fast path, bypassing the explicit buffer",
			newReader: func() io.Reader { return bytes.NewReader(body) },
		},
		{
			name: "onlyReader hides WriteTo so io.CopyBuffer must drive the chunked copy loop through checksumBufSize",
			newReader: func() io.Reader {
				return &onlyReader{Reader: bytes.NewReader(body)}
			},
			checkReads: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := c.newReader()
			sum, n, err := Checksum(r)
			if err != nil {
				t.Fatalf("Checksum err = %v", err)
			}
			if sum != want {
				t.Errorf("Checksum = %q, want %q", sum, want)
			}
			if n != int64(bodySize) {
				t.Errorf("byte count = %d, want %d", n, bodySize)
			}
			if c.checkReads {
				or := r.(*onlyReader)
				if or.reads <= 1 {
					t.Errorf("Read call count = %d, want > 1 (the chunked copy loop must actually iterate)", or.reads)
				}
				t.Logf("onlyReader observed %d Read call(s) for a %d-byte body against a %d-byte buffer", or.reads, bodySize, checksumBufSize)
			}
		})
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

// onlyReader wraps a reader and exposes nothing but Read, so io.CopyBuffer
// cannot take its io.WriterTo fast path (io/io.go:407-416) even when the
// wrapped reader implements WriteTo (as *bytes.Reader and *strings.Reader
// do). Embedding the io.Reader INTERFACE, not a concrete reader type, is
// what hides WriteTo: a struct only promotes the methods declared on its
// embedded field's static type, and io.Reader declares no WriteTo. This is
// what lets a test actually drive checksumBufSize-sized reads through
// Checksum's chunked copy loop instead of a specialized WriteTo. Local to
// this file; never redeclared.
type onlyReader struct {
	io.Reader
	reads int
}

func (o *onlyReader) Read(p []byte) (int, error) {
	o.reads++
	return o.Reader.Read(p)
}

var _ io.Reader = (*errReader)(nil)
var _ io.Writer = (*errWriter)(nil)
var _ io.Reader = (*onlyReader)(nil)
