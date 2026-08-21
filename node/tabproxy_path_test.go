package main

import (
	"net/url"
	"testing"
)

// Both proxy paths build their URL by concatenation, so a path that is not
// origin-form does not stay a path. Core sanitizes, but Core and the node are
// deployed and versioned separately and this is the side holding the network
// position.
func TestSafeProxyPath(t *testing.T) {
	ok := []string{"/", "/index.html", "/a/b?c=d", "/x#y", "/@notahost"}
	bad := []string{
		"",
		"@evil.com/x",        // becomes userinfo + a new host
		":@evil.com/",        //
		"evil.com/x",         // relative: the last path segment is replaced
		"//evil.com/x",       // protocol-relative
		"http://evil.com/x",  //
		"\\\\evil.com\\x",    //
		" /still-not-a-path", //
	}

	for _, p := range ok {
		if !safeProxyPath(p) {
			t.Errorf("safeProxyPath(%q) = false, want true", p)
		}
	}
	for _, p := range bad {
		if safeProxyPath(p) {
			t.Errorf("safeProxyPath(%q) = true, want false", p)
		}
	}
}

// Show the consequence rather than assert the rule twice: the rejected forms
// are exactly the ones that move the host when concatenated.
func TestARejectedPathWouldHaveMovedTheHost(t *testing.T) {
	const addr = "10.0.0.5:8080"
	for _, p := range []string{"@evil.com/x", "evil.com/x", "http://evil.com/x"} {
		u, err := url.Parse("http://" + addr + p)
		if err != nil {
			continue
		}
		if u.Host == addr {
			t.Errorf("%q was expected to move the host but parsed to %s; the guard may be over-broad", p, u.Host)
		}
		if safeProxyPath(p) {
			t.Errorf("%q moves the host to %s and is still accepted", p, u.Host)
		}
	}
	u, err := url.Parse("http://" + addr + "/a/b?c=d")
	if err != nil || u.Host != addr {
		t.Fatalf("a legitimate path changed the host: %v %v", u, err)
	}
}
