package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A host that accepts the connection and then never answers is the failure the
// bare &http.Client{} could not see: Do() stays pending, so the UI shows a
// download that never starts and never fails.
func TestDownloadDoesNotHangOnAHostThatNeverAnswers(t *testing.T) {
	// Never sends response headers, and returns as soon as the client hangs up -
	// httptest.Server.Close waits for outstanding handlers, so a plain Sleep here
	// would add its full duration to the package's test time.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	restore := downloadHTTPClient
	downloadHTTPClient = newDownloadClient(time.Second, time.Second, 250*time.Millisecond)
	defer func() { downloadHTTPClient = restore }()

	c := &CoreClient{apiURL: srv.URL, token: "t"}

	done := make(chan error, 1)
	go func() {
		rc, err := c.DownloadFile("/x", "srv-1")
		if rc != nil {
			rc.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stalled host produced no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DownloadFile never returned; the download transport is unbounded again")
	}
}

// The other half of the decision: the BODY must stay unbounded, or a multi-GB
// world download dies partway through. An overall Client.Timeout would do
// exactly that, which is why the old code reached for a bare client.
func TestTheDownloadClientDoesNotCapTheTransferItself(t *testing.T) {
	if downloadHTTPClient.Timeout != 0 {
		t.Errorf("downloadHTTPClient.Timeout = %v, want 0; an overall timeout caps the whole transfer",
			downloadHTTPClient.Timeout)
	}
	// This is what catches a return to `&http.Client{}`: a bare client has a nil
	// Transport, so it bounds neither the handshake nor the response headers, and
	// the test above cannot see it because that one substitutes its own client to
	// keep the timings short.
	tr, ok := downloadHTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatal("downloadHTTPClient has no explicit transport; it bounds nothing before the body")
	}
	if tr.TLSHandshakeTimeout == 0 || tr.ResponseHeaderTimeout == 0 {
		t.Errorf("handshake=%v responseHeader=%v; both phases must be bounded",
			tr.TLSHandshakeTimeout, tr.ResponseHeaderTimeout)
	}
}

// A large body must still stream through untouched, past any per-phase bound.
func TestALargeBodyStreamsThrough(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 3<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(payload[:1])
		w.(http.Flusher).Flush()
		time.Sleep(400 * time.Millisecond) // longer than the response-header bound below
		w.Write(payload[1:])
	}))
	defer srv.Close()

	restore := downloadHTTPClient
	downloadHTTPClient = newDownloadClient(time.Second, time.Second, 250*time.Millisecond)
	defer func() { downloadHTTPClient = restore }()

	c := &CoreClient{apiURL: srv.URL, token: "t"}
	rc, err := c.DownloadFile("/big", "srv-1")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, rc)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if n != int64(len(payload)) {
		t.Errorf("read %d bytes, want %d; a slow body was cut off", n, len(payload))
	}
}

// BEAM_TLS_SKIP_VERIFY turns off certificate verification on the hop that
// carries the beam ticket. Its two siblings are loud about the same decision -
// ConnectBeamNodeDirect refuses to dial unpinned, Core logs a WARNING for
// GRPC_TLS_ENABLED=false - and this one said nothing at all.
func TestSkipVerifyIsNotSilent(t *testing.T) {
	t.Setenv("BEAM_TLS_SKIP_VERIFY", "true")
	skipVerifyWarnOnce = sync.Once{}

	var buf bytes.Buffer
	restore := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(restore)

	if !relayTLSSkipVerify() {
		t.Fatal(`BEAM_TLS_SKIP_VERIFY="true" was not honoured`)
	}
	if !strings.Contains(buf.String(), "BEAM_TLS_SKIP_VERIFY") {
		t.Error("skipping TLS verification produced no warning")
	}

	// Once per process: the dial path calls this on every connection, and a
	// line per connection would train the reader to ignore it.
	buf.Reset()
	relayTLSSkipVerify()
	if buf.Len() != 0 {
		t.Errorf("the warning repeated: %q", buf.String())
	}
}

func TestSkipVerifyOffByDefault(t *testing.T) {
	for _, v := range []string{"", "false", "0", "yes", "TRUE"} {
		t.Setenv("BEAM_TLS_SKIP_VERIFY", v)
		if relayTLSSkipVerify() {
			t.Errorf("BEAM_TLS_SKIP_VERIFY=%q disabled certificate verification", v)
		}
	}
}
