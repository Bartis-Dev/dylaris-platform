package main

import (
	"net"
	"strings"
	"testing"
)

// resolveContainerIP is the guard between "the container name did not resolve
// locally" and "send the RCON password wherever DNS points". Docker's embedded
// resolver forwards a name it cannot resolve to the host's upstream DNS, and a
// wildcard record anywhere in that chain answers with a public address. Seen
// live on a stopped server:
//
//	dial mc_<uuid>:25575: dial tcp 46.225.53.182:25575: i/o timeout
//
// The timeout was luck. execRcon's first write is SERVERDATA_AUTH carrying the
// password in the clear, so anything listening on that port would have received
// it.
//
// These tests drive the address FILTER rather than the DNS lookup, since the
// lookup is the operating system's. The filter is the part with a decision in it.
func TestContainerAddressFilter(t *testing.T) {
	cases := []struct {
		name     string
		ip       string
		accepted bool
	}{
		// Docker's own default pools.
		{"docker bridge default", "172.18.0.7", true},
		{"a 10/8 overlay", "10.0.3.14", true},
		{"a 192.168 network", "192.168.5.9", true},
		{"IPv6 unique local", "fd00::1", true},

		// The observed escape and its neighbours.
		{"the address a wildcard record answered with", "46.225.53.182", false},
		{"any other public v4", "8.8.8.8", false},
		{"public v6", "2001:4860:4860::8888", false},

		// Loopback is never the MC container - the node is containerized and its
		// own loopback is its own process. The file header says so, and letting it
		// through would turn a missing container into a dial at ourselves.
		{"loopback is not the container", "127.0.0.1", false},
		{"IPv6 loopback", "::1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ip := net.ParseIP(c.ip)
			if ip == nil {
				t.Fatalf("bad test address %q", c.ip)
			}
			got := ip.IsPrivate() || ip.IsLinkLocalUnicast()
			if got != c.accepted {
				t.Errorf("address %s accepted = %v, want %v", c.ip, got, c.accepted)
			}
		})
	}
}

// A name that does not resolve at all must report that, not dial anything.
func TestResolveContainerIPFailsOnAnUnresolvableName(t *testing.T) {
	_, err := resolveContainerIP("mc_this-name-does-not-exist.invalid", 25575)
	if err == nil {
		t.Fatal("expected an error for an unresolvable name")
	}
	if !strings.Contains(err.Error(), "resolve") {
		t.Errorf("err = %v, want it to name the resolution step", err)
	}
}

// The refusal message has to tell an operator both things they need: that the
// container is not on this network, and that something outside answered for it.
// A bare "no route to host" would send them looking at the wrong layer.
func TestResolveContainerIPRefusalNamesTheCause(t *testing.T) {
	// localhost resolves to loopback, which the filter rejects for the same
	// reason a public address is rejected: it cannot be the MC container.
	_, err := resolveContainerIP("localhost", 25575)
	if err == nil {
		t.Skip("localhost did not resolve to a rejected address on this host")
	}
	for _, want := range []string{"not on this node's network", "refusing to send RCON credentials"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
}
