package services

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestIsDisallowedIP(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		{"loopback v4", "127.0.0.1", true},
		{"loopback v6", "::1", true},
		{"rfc1918 10.0.0.0/8", "10.0.0.1", true},
		{"rfc1918 172.16.0.0/12 low", "172.16.5.4", true},
		{"rfc1918 172.16.0.0/12 high boundary", "172.31.255.255", true},
		{"just above 172.16.0.0/12 is public", "172.32.0.1", false},
		{"rfc1918 192.168.0.0/16", "192.168.1.1", true},
		{"cgnat lower bound", "100.64.0.1", true},
		{"cgnat upper bound", "100.127.255.254", true},
		{"just below cgnat range is public", "100.63.255.255", false},
		{"just above cgnat range is public", "100.128.0.1", false},
		{"link-local v4", "169.254.1.1", true},
		{"cloud metadata endpoint", "169.254.169.254", true},
		{"ipv4 link-local multicast", "224.0.0.1", true},
		{"ipv4 general multicast", "230.1.2.3", true},
		{"ipv6 ULA fc00::/7", "fc00::1", true},
		{"ipv6 link-local fe80::/10", "fe80::1", true},
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v6", "::", true},
		{"ipv4-mapped ipv6 private", "::ffff:10.0.0.1", true},
		{"ipv4-mapped ipv6 public", "::ffff:8.8.8.8", false},
		{"public v4", "8.8.8.8", false},
		{"public v4 other", "1.1.1.1", false},
		{"public v6", "2606:4700:4700::1111", false},
		{"unparseable/nil ip", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if got := isDisallowedIP(ip); got != tc.want {
				t.Errorf("isDisallowedIP(%q) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestSafeFetch_BadScheme(t *testing.T) {
	_, err := SafeFetch(context.Background(), "ftp://example.com/file", 1024, time.Second)
	if err == nil || !strings.Contains(err.Error(), "scheme not allowed") {
		t.Fatalf("expected a scheme error, got %v", err)
	}
}

func TestSafeFetch_MissingHost(t *testing.T) {
	_, err := SafeFetch(context.Background(), "http:///no-host", 1024, time.Second)
	if err == nil || !strings.Contains(err.Error(), "missing host") {
		t.Fatalf("expected a missing-host error, got %v", err)
	}
}

// allowDialTarget temporarily lets safeFetchDialer's Control func through for
// exactly one address (a local httptest.Server, otherwise blocked as
// loopback), restoring the real check on cleanup. This is test-only,
// same-package white-box wiring (the _test.go file never ships) - it does
// NOT weaken isDisallowedIP itself, which has its own full table test above.
func allowDialTarget(t *testing.T, allowedAddr string) {
	t.Helper()
	orig := safeFetchDialer.Control
	safeFetchDialer.Control = func(network, address string, c syscall.RawConn) error {
		if address == allowedAddr {
			return nil
		}
		return orig(network, address, c)
	}
	t.Cleanup(func() { safeFetchDialer.Control = orig })
}

func TestSafeFetch_SizeCap(t *testing.T) {
	const bodyLen = 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("a", bodyLen)))
	}))
	defer srv.Close()
	allowDialTarget(t, srv.Listener.Addr().String())

	cases := []struct {
		name     string
		maxBytes int64
		wantErr  bool
	}{
		{"under cap", 100, false},
		{"exact cap", bodyLen, false},
		{"over cap by one", bodyLen - 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := SafeFetch(context.Background(), srv.URL, tc.maxBytes, 2*time.Second)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "byte cap") {
					t.Fatalf("expected a cap error, got body=%q err=%v", body, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(body) != bodyLen {
				t.Fatalf("expected %d bytes, got %d", bodyLen, len(body))
			}
		})
	}
}

func TestSafeFetch_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	allowDialTarget(t, srv.Listener.Addr().String())

	_, err := SafeFetch(context.Background(), srv.URL, 1024, 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("expected a status error, got %v", err)
	}
}

func TestSafeFetch_RedirectToDisallowedHost(t *testing.T) {
	// target exists only so its address is a real, dialable loopback
	// address; it must NEVER receive a request in this test.
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte("should never be reached"))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	// Only the redirector's own address is allowlisted. target's address is
	// a DIFFERENT loopback port and stays subject to the real
	// isDisallowedIP check, so the redirect hop must be blocked exactly as
	// a DNS-rebind to a private address would be.
	allowDialTarget(t, redirector.Listener.Addr().String())

	_, err := SafeFetch(context.Background(), redirector.URL, 1024, 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "blocked non-public address") {
		t.Fatalf("expected the redirect to be blocked, got err=%v", err)
	}
	if reached {
		t.Fatal("the disallowed redirect target must never receive a request")
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestSafeFetchClient_CheckRedirect(t *testing.T) {
	mkVia := func(n int) []*http.Request {
		via := make([]*http.Request, n)
		for i := range via {
			via[i] = &http.Request{}
		}
		return via
	}
	httpsReq := &http.Request{URL: mustParseURL(t, "https://example.com/x")}
	ftpReq := &http.Request{URL: mustParseURL(t, "ftp://example.com/x")}

	if err := safeFetchClient.CheckRedirect(httpsReq, mkVia(4)); err != nil {
		t.Errorf("expected 4 prior hops to be allowed, got %v", err)
	}
	if err := safeFetchClient.CheckRedirect(httpsReq, mkVia(5)); err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Errorf("expected a too-many-redirects error at 5 prior hops, got %v", err)
	}
	if err := safeFetchClient.CheckRedirect(ftpReq, mkVia(0)); err == nil || !strings.Contains(err.Error(), "disallowed redirect scheme") {
		t.Errorf("expected a disallowed-scheme error for an ftp redirect, got %v", err)
	}
}
