package handlers

import "testing"

// The whole point of resolving Redis here is that a warp spoke has no DNS into
// the cluster: copying Core's own "redis:6379" into a customer's compose file
// produces a node that starts, never resolves, and reports nothing. So a
// service name that cannot be resolved must come back EMPTY - the snippet then
// keeps its placeholder, which at least tells the reader something is missing.
func TestOverlayRedisAddr(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "unset", in: "", want: ""},
		{name: "whitespace only", in: "   ", want: ""},
		{
			name: "literal ip keeps its port",
			in:   "10.20.0.5:6379",
			want: "10.20.0.5:6379",
		},
		{
			// Core's REDIS_ADDR is allowed to omit the port; the node needs one.
			name: "bare ip gets the default port",
			in:   "10.20.0.5",
			want: "10.20.0.5:6379",
		},
		{
			name: "a non-default port survives",
			in:   "10.20.0.5:6380",
			want: "10.20.0.5:6380",
		},
		{
			// A name that resolves nowhere is the case that used to be copied
			// verbatim into the snippet.
			name: "an unresolvable name yields nothing, not the name",
			in:   "redis-that-does-not-exist.invalid:6379",
			want: "",
		},
		{
			// IPv6 has no path through the overlay snippets today; answering
			// with one would be a value the node cannot use.
			name: "ipv6 is not offered",
			in:   "[::1]:6379",
			want: "",
		},
		{
			name: "localhost resolves to its v4 form",
			in:   "127.0.0.1:6379",
			want: "127.0.0.1:6379",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := overlayRedisAddr(tt.in); got != tt.want {
				t.Errorf("overlayRedisAddr(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
