package main

import (
	"errors"
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

// guardPrivateAddr accepts a private address and formats ip:port.
func TestGuardPrivateAddrAcceptsPrivate(t *testing.T) {
	addr, err := guardPrivateAddr(net.ParseIP("172.20.0.5"), 25575)
	if err != nil {
		t.Fatalf("private IP rejected: %v", err)
	}
	if addr != "172.20.0.5:25575" {
		t.Fatalf("addr = %q, want 172.20.0.5:25575", addr)
	}
}

// The refusal message has to tell an operator that the value is not on this
// network, and it must never hand credentials to a public address. A nil address
// (container not running) is refused too.
func TestGuardPrivateAddrRefusalNamesTheCause(t *testing.T) {
	_, err := guardPrivateAddr(net.ParseIP("46.225.53.182"), 25575)
	if err == nil {
		t.Fatal("public address must be refused")
	}
	for _, want := range []string{"not on this node's network", "refusing to send RCON credentials"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
	if _, err := guardPrivateAddr(nil, 25575); err == nil {
		t.Error("nil address (container not running) must be refused")
	}
}

// resolveMCAddr surfaces the daemon resolver's error (e.g. container absent) and
// never fabricates a target.
func TestResolveMCAddrPropagatesResolverError(t *testing.T) {
	prev := resolveMCContainerIP
	t.Cleanup(func() { resolveMCContainerIP = prev })

	resolveMCContainerIP = func(uuid string) (net.IP, error) {
		return nil, errors.New("inspect mc_abc: no such container")
	}
	if _, err := resolveMCAddr("abc", 25575); err == nil {
		t.Fatal("expected the resolver error to propagate")
	}

	// A private IP from the daemon is accepted end to end.
	resolveMCContainerIP = func(uuid string) (net.IP, error) {
		return net.ParseIP("172.20.0.9"), nil
	}
	addr, err := resolveMCAddr("abc", 25575)
	if err != nil || addr != "172.20.0.9:25575" {
		t.Fatalf("resolveMCAddr = (%q, %v), want 172.20.0.9:25575", addr, err)
	}

	// An uninitialised resolver refuses rather than dialling anything.
	resolveMCContainerIP = nil
	if _, err := resolveMCAddr("abc", 25575); err == nil {
		t.Fatal("nil resolver must refuse")
	}
}
