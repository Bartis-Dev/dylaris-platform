package services

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeRoundTripper answers any request with a canned body, so a test can reach
// StreamModrinthJar's logic past the hard cdn.modrinth.com allowlist (the URL
// still has to carry the prefix; only the transport is swapped).
type fakeRoundTripper struct {
	status int
	body   []byte
}

func (f fakeRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: f.status,
		Body:       io.NopCloser(bytes.NewReader(f.body)),
		Header:     make(http.Header),
	}, nil
}

func withFakeModrinthClient(t *testing.T, status int, body []byte) {
	t.Helper()
	prev := modrinthDownloadClient
	t.Cleanup(func() { modrinthDownloadClient = prev })
	modrinthDownloadClient = &http.Client{Transport: fakeRoundTripper{status: status, body: body}}
}

func sha1hex(b []byte) string {
	s := sha1.Sum(b)
	return hex.EncodeToString(s[:])
}

const cdnURL = "https://cdn.modrinth.com/data/AB/versions/1/mod.jar"

// TestStreamModrinthJar_StreamsAndVerifies is the core: it writes the body to
// dst without buffering the whole jar and passes when the hash matches.
func TestStreamModrinthJar_StreamsAndVerifies(t *testing.T) {
	body := bytes.Repeat([]byte("m"), 4096)
	withFakeModrinthClient(t, http.StatusOK, body)

	var dst bytes.Buffer
	n, err := StreamModrinthJar(context.Background(), cdnURL, &dst, sha1hex(body), "")
	if err != nil {
		t.Fatalf("StreamModrinthJar: %v", err)
	}
	if n != int64(len(body)) {
		t.Errorf("bytes = %d, want %d", n, len(body))
	}
	if !bytes.Equal(dst.Bytes(), body) {
		t.Errorf("dst did not receive the streamed body intact")
	}
}

// TestStreamModrinthJar_RejectsAHashMismatch is the integrity guard: a jar whose
// bytes do not match the expected hash is an error, so the caller (which streams
// against a scratch temp file) never promotes it into the output.
func TestStreamModrinthJar_RejectsAHashMismatch(t *testing.T) {
	withFakeModrinthClient(t, http.StatusOK, []byte("actual bytes"))

	var dst bytes.Buffer
	_, err := StreamModrinthJar(context.Background(), cdnURL, &dst, sha1hex([]byte("different")), "")
	if err == nil {
		t.Fatal("StreamModrinthJar accepted a body whose sha1 did not match, want an error")
	}
	if !strings.Contains(err.Error(), "sha1 mismatch") {
		t.Errorf("error = %v, want a sha1 mismatch", err)
	}
}

// TestStreamModrinthJar_RefusesNonCDNURL keeps the hard allowlist: the render
// must never be coerced into fetching an arbitrary URL from a content entry.
func TestStreamModrinthJar_RefusesNonCDNURL(t *testing.T) {
	var dst bytes.Buffer
	_, err := StreamModrinthJar(context.Background(), "https://evil.example/mod.jar", &dst, "", "")
	if err == nil {
		t.Fatal("StreamModrinthJar fetched a non-CDN URL, want a refusal")
	}
	if dst.Len() != 0 {
		t.Errorf("wrote %d bytes for a refused URL, want 0", dst.Len())
	}
}
