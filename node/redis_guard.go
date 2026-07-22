package main

import (
	"net"
	"strings"
)

// redisAddrIsolationSafe reports whether SIDECAR_REDIS_ADDR (the address baked
// into isolated MC containers) is reachable WITHOUT Docker DNS on the shared
// dylaris_net. Isolated servers leave dylaris_net, so a Docker-DNS-only address
// (a bare single-label name like "redis") would leave the log-shipper unable to
// reach Redis. Fails safe: anything not clearly host-reachable returns false, so
// the node keeps that node's servers on dylaris_net instead of isolating them.
func redisAddrIsolationSafe(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true // explicit IP: routable off the Docker network
	}
	if host == "host.docker.internal" {
		return true // Docker's host-gateway alias
	}
	// A dotted name is a real (resolvable) FQDN. A bare single-label name
	// (e.g. "redis", "valkey") only resolves via Docker DNS on a shared net.
	return strings.Contains(host, ".")
}
