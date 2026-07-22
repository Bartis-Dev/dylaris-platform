package main

import "testing"

func TestRedisAddrIsolationSafe(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want bool
	}{
		{"empty is unsafe", "", false},
		{"bare single-label name is dns-only", "redis", false},
		{"bare name with port is dns-only", "redis:6379", false},
		{"other bare service name is dns-only", "valkey:6379", false},
		{"private ipv4 with port is safe", "10.0.0.5:6379", true},
		{"loopback ipv4 is safe", "127.0.0.1:6379", true},
		{"public ipv4 is safe", "94.130.98.3:6379", true},
		{"docker host gateway alias is safe", "host.docker.internal:6379", true},
		{"dotted fqdn is safe", "redis.internal.example.com:6379", true},
		{"bracketed ipv6 with port is safe", "[::1]:6379", true},
		{"whitespace is trimmed", "  10.0.0.5:6379  ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := redisAddrIsolationSafe(c.addr); got != c.want {
				t.Fatalf("redisAddrIsolationSafe(%q) = %v, want %v", c.addr, got, c.want)
			}
		})
	}
}
