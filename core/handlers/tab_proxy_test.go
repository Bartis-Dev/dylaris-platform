package handlers

import (
	"net/http"
	"strings"
	"testing"

	pb "dylaris-proto/node"
)

func TestInjectBaseHref(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		base         string
		wantContains string
	}{
		{"after head", "<html><head><title>x</title></head></html>", "/p/", `<head><base href="/p/">`},
		{"head with attrs", `<head lang="en"><title>x</title>`, "/p/", `<head lang="en"><base href="/p/">`},
		{"no head prepends", "<html><body>x</body></html>", "/p/", `<base href="/p/"><html>`},
		{"uppercase HEAD", "<HTML><HEAD></HEAD>", "/p/", `<base href="/p/">`},
	}
	for _, c := range cases {
		got := string(injectBaseHref([]byte(c.in), c.base))
		if !strings.Contains(got, c.wantContains) {
			t.Errorf("%s: injectBaseHref = %q, want contains %q", c.name, got, c.wantContains)
		}
	}
}

func TestCoreStripHopByHop(t *testing.T) {
	in := []*pb.HttpHeader{
		{Key: "Content-Type", Value: "text/html"},
		{Key: "Connection", Value: "keep-alive"},
		{Key: "Transfer-Encoding", Value: "chunked"},
		{Key: "Upgrade", Value: "websocket"},
		{Key: "X-Keep", Value: "yes"},
	}
	got := coreStripHopByHop(in)
	kept := map[string]bool{}
	for _, h := range got {
		kept[h.Key] = true
	}
	for _, drop := range []string{"Connection", "Transfer-Encoding", "Upgrade"} {
		if kept[drop] {
			t.Errorf("hop-by-hop %q not stripped", drop)
		}
	}
	for _, keep := range []string{"Content-Type", "X-Keep"} {
		if !kept[keep] {
			t.Errorf("header %q dropped", keep)
		}
	}
}

func TestIsWebSocketUpgrade(t *testing.T) {
	cases := []struct {
		name       string
		upgrade    string
		connection string
		want       bool
	}{
		{"ws upgrade", "websocket", "Upgrade", true},
		{"ws upgrade mixed case", "WebSocket", "keep-alive, Upgrade", true},
		{"no upgrade header", "", "", false},
		{"upgrade but not ws", "h2c", "Upgrade", false},
		{"ws but connection missing", "websocket", "", false},
	}
	for _, c := range cases {
		r, _ := http.NewRequest("GET", "/x", nil)
		if c.upgrade != "" {
			r.Header.Set("Upgrade", c.upgrade)
		}
		if c.connection != "" {
			r.Header.Set("Connection", c.connection)
		}
		if got := isWebSocketUpgrade(r); got != c.want {
			t.Errorf("%s: isWebSocketUpgrade = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSingleJoin(t *testing.T) {
	cases := []struct {
		base, sub, want string
	}{
		{"/", "", "/"},
		{"/", "tiles/x.png", "/tiles/x.png"},
		{"/map", "tiles", "/map/tiles"},
		{"/map/", "/tiles", "/map/tiles"},
		{"/map", "/tiles", "/map/tiles"},
	}
	for _, c := range cases {
		if got := singleJoin(c.base, c.sub); got != c.want {
			t.Errorf("singleJoin(%q,%q) = %q, want %q", c.base, c.sub, got, c.want)
		}
	}
}

// TestSanitizeProxyPath covers security invariant #2 carried forward from the
// WS5 task reviews: the path Core forwards to the node in HttpProxyReq.Path
// MUST be origin-form starting with exactly one "/", never "//", so a value
// like "//evil.com/x" can't be mistaken for a scheme-relative URL by any
// consumer that re-parses it, and must never carry a raw CR/LF that could
// enable request-line/header injection when the node concatenates it into a
// raw URL string ("http://" + addr + path, see node/grpc_tabproxy.go).
func TestSanitizeProxyPath(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"already clean", "/a/b", "/a/b"},
		{"missing leading slash", "a/b", "/a/b"},
		{"empty", "", "/"},
		{"double leading slash", "//evil.com/x", "/evil.com/x"},
		{"triple leading slash", "///evil.com", "/evil.com"},
		{"scheme-relative attempt with query", "//evil.com/x?y=1", "/evil.com/x?y=1"},
		{"CR LF stripped", "/a\r\nEvil-Header: 1", "/aEvil-Header: 1"},
		{"embedded double slash mid path kept", "/a//b", "/a//b"},
		{"at-sign userinfo-style attempt", "/@evil.com/x", "/@evil.com/x"},
	}
	for _, c := range cases {
		if got := sanitizeProxyPath(c.in); got != c.want {
			t.Errorf("%s: sanitizeProxyPath(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
