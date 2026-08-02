package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The failure these clients exist for: a peer that accepts the connection and
// then never responds. With http.DefaultClient that hangs forever and the server
// stays in `installing` with no outcome either way.
func TestInstallerMetaClient_TimesOutOnASilentServer(t *testing.T) {
	stall := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-stall // accept, then say nothing
	}))
	// Order matters and is LIFO: srv.Close() blocks until its handlers return,
	// so the channel has to be closed FIRST or the test deadlocks on its own
	// cleanup. Declaring the close last is what makes it run first.
	defer srv.Close()
	defer close(stall)

	// A local copy so the test does not wait out the production 30s.
	client := &http.Client{Timeout: 150 * time.Millisecond}

	done := make(chan error, 1)
	go func() {
		resp, err := client.Get(srv.URL)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a silent server returned no error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the request hung despite a client timeout")
	}
}

// The metadata client must have an overall deadline; that is its whole point.
func TestInstallerMetaClient_HasATimeout(t *testing.T) {
	if installerMetaClient.Timeout <= 0 {
		t.Fatal("installerMetaClient has no timeout - http.DefaultClient's behaviour is what this replaced")
	}
	if installerMetaClient.Timeout != installerMetaTimeout {
		t.Errorf("timeout = %v, want %v", installerMetaClient.Timeout, installerMetaTimeout)
	}
}

// The download client must NOT have an overall deadline: a server JAR on a thin
// link legitimately takes minutes, and a blanket timeout would abort a transfer
// that is making progress.
func TestInstallerDownloadClient_HasNoOverallDeadline(t *testing.T) {
	if installerDownloadClient.Timeout != 0 {
		t.Fatalf("installerDownloadClient.Timeout = %v, want 0 - it would cut off slow but working downloads",
			installerDownloadClient.Timeout)
	}
}

// It must still bound the phases that can hang without transferring anything,
// or it would be no better than http.DefaultClient for a stalled peer.
func TestInstallerDownloadClient_BoundsTheStallablePhases(t *testing.T) {
	tr, ok := installerDownloadClient.Transport.(*http.Transport)
	if !ok {
		t.Fatal("installerDownloadClient has no *http.Transport, so nothing bounds a stalled connection")
	}
	if tr.TLSHandshakeTimeout <= 0 {
		t.Error("TLSHandshakeTimeout unset")
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Error("ResponseHeaderTimeout unset - a peer that accepts and never answers would hang")
	}
	if tr.DialContext == nil {
		t.Error("DialContext unset, so connecting is unbounded")
	}
}

// A custom Transport starts from the ZERO value, not from DefaultTransport, so
// a nil Proxy means HTTP_PROXY/HTTPS_PROXY are ignored - every download on a
// node behind a proxy would fail, and only there, which is the worst kind of
// regression to find. The meta client keeps DefaultTransport and needs no check.
//
// Asserting the hook is wired is the whole test: resolving an actual proxy URL
// would be testing net/http's env parsing, which caches the environment in a
// sync.Once and so cannot be driven from a test anyway.
func TestInstallerDownloadClient_HonoursProxyEnvironment(t *testing.T) {
	tr, ok := installerDownloadClient.Transport.(*http.Transport)
	if !ok {
		t.Fatal("installerDownloadClient has no *http.Transport")
	}
	if tr.Proxy == nil {
		t.Fatal("Transport.Proxy is nil - HTTP_PROXY/HTTPS_PROXY would be ignored")
	}
}

// A response-header stall is the download client's specific hazard: bytes never
// start flowing, so no progress-based rule would ever fire.
func TestInstallerDownloadClient_ResponseHeaderStall(t *testing.T) {
	stall := make(chan struct{})
	defer close(stall)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		<-stall // connection accepted, no response line ever sent
	}()

	client := &http.Client{Transport: &http.Transport{
		ResponseHeaderTimeout: 150 * time.Millisecond,
	}}

	done := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://" + ln.Addr().String())
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a header-stalling server returned no error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the request hung despite ResponseHeaderTimeout")
	}
}
