package handlers

import (
	"errors"
	"net"
	"testing"
)

// The whole point of resolving Redis here is that a warp spoke has no DNS into
// the cluster: copying Core's own "redis:6379" into a customer's compose file
// produces a node that starts, never resolves, and reports nothing.
//
// The resolver is injected because the answer must not depend on the DNS of
// whatever machine runs this - CI sits behind one that answers NXDOMAIN with a
// public address of its own, which is exactly the case the private-only rule
// below exists to reject.
func TestResolveOverlayRedisAddr(t *testing.T) {
	noLookup := func(string) ([]net.IP, error) {
		t.Helper()
		t.Error("a literal address must not be resolved")
		return nil, nil
	}
	fails := func(string) ([]net.IP, error) { return nil, errors.New("no such host") }
	answers := func(ips ...string) func(string) ([]net.IP, error) {
		return func(string) ([]net.IP, error) {
			out := make([]net.IP, 0, len(ips))
			for _, s := range ips {
				out = append(out, net.ParseIP(s))
			}
			return out, nil
		}
	}

	tests := []struct {
		name   string
		in     string
		lookup func(string) ([]net.IP, error)
		want   string
	}{
		{name: "unset", in: "", lookup: noLookup, want: ""},
		{name: "whitespace only", in: "   ", lookup: noLookup, want: ""},
		{
			name:   "literal private ip keeps its port",
			in:     "10.20.0.5:6379",
			lookup: noLookup,
			want:   "10.20.0.5:6379",
		},
		{
			// Core's REDIS_ADDR is allowed to omit the port; the node needs one.
			name:   "bare ip gets the default port",
			in:     "10.20.0.5",
			lookup: noLookup,
			want:   "10.20.0.5:6379",
		},
		{
			name:   "a non-default port survives",
			in:     "10.20.0.5:6380",
			lookup: noLookup,
			want:   "10.20.0.5:6380",
		},
		{
			// The case that used to be copied verbatim into the snippet.
			name:   "a name that does not resolve yields nothing, not the name",
			in:     "redis:6379",
			lookup: fails,
			want:   "",
		},
		{
			name:   "a service name resolves to its overlay address",
			in:     "redis:6379",
			lookup: answers("10.20.0.5"),
			want:   "10.20.0.5:6379",
		},
		{
			// A resolver that answers every name with a public landing address
			// would otherwise put a stranger's IP into a customer's compose file.
			name:   "a hijacked NXDOMAIN answer is refused",
			in:     "redis:6379",
			lookup: answers("46.225.53.182"),
			want:   "",
		},
		{
			name:   "the private answer is preferred over a public one",
			in:     "redis:6379",
			lookup: answers("46.225.53.182", "10.20.0.5"),
			want:   "10.20.0.5:6379",
		},
		{
			// Never reachable from the machine that will read this.
			name:   "loopback is refused",
			in:     "127.0.0.1:6379",
			lookup: noLookup,
			want:   "",
		},
		{
			// IPv6 has no path through the overlay snippets today; answering
			// with one would be a value the node cannot use.
			name:   "ipv6 is not offered",
			in:     "[fd00::1]:6379",
			lookup: noLookup,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveOverlayRedisAddr(tt.in, tt.lookup); got != tt.want {
				t.Errorf("resolveOverlayRedisAddr(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Core's gRPC address goes through the same resolver, so what has to be pinned
// here is the part that differs: the service NAME carries no port, and the port
// comes from DYLARIS_GRPC_PORT rather than a Redis default.
//
// The reason this resolves a name at all: Core used to hand out its own task IP,
// which changes on every redeploy and names one replica out of however many are
// running - while the Redis address beside it in the same snippet had always
// been a resolved service name.
func TestResolveOverlayAddrForCore(t *testing.T) {
	answers := func(ips ...string) func(string) ([]net.IP, error) {
		return func(string) ([]net.IP, error) {
			out := make([]net.IP, 0, len(ips))
			for _, s := range ips {
				out = append(out, net.ParseIP(s))
			}
			return out, nil
		}
	}
	fails := func(string) ([]net.IP, error) { return nil, errors.New("no such host") }

	tests := []struct {
		name        string
		in          string
		defaultPort string
		lookup      func(string) ([]net.IP, error)
		want        string
	}{
		{
			name:        "the service name takes the gRPC port",
			in:          "core",
			defaultPort: "25501",
			lookup:      answers("10.20.0.3"),
			want:        "10.20.0.3:25501",
		},
		{
			name:        "a non-default gRPC port is carried through",
			in:          "core",
			defaultPort: "25555",
			lookup:      answers("10.20.0.3"),
			want:        "10.20.0.3:25555",
		},
		{
			// A single-container install where "core" means nothing. deployAddrs
			// falls back to the local address; here the honest answer is "".
			name:        "a name that does not resolve yields nothing",
			in:          "core",
			defaultPort: "25501",
			lookup:      fails,
			want:        "",
		},
		{
			// Same rule as Redis: this ends up in a customer's compose file.
			name:        "a hijacked NXDOMAIN answer is refused",
			in:          "core",
			defaultPort: "25501",
			lookup:      answers("46.225.53.182"),
			want:        "",
		},
		{
			// CORE_SERVICE_NAME is free text; an operator who writes a port in it
			// meant it, and it wins over the default.
			name:        "an explicit port in the name wins",
			in:          "core:9999",
			defaultPort: "25501",
			lookup:      answers("10.20.0.3"),
			want:        "10.20.0.3:9999",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveOverlayAddr(tt.in, tt.defaultPort, tt.lookup); got != tt.want {
				t.Errorf("resolveOverlayAddr(%q, %q) = %q, want %q", tt.in, tt.defaultPort, got, tt.want)
			}
		})
	}
}
