package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
)

// A machine joining the overlay has to be told two addresses that only exist
// inside it: Core's gRPC and Redis. Core knows both - it is talking to Redis
// right now and it is itself on the same network - but until this endpoint the
// panel handed out <core-grpc e.g. 10.20.0.4:25501> and left the customer to
// find the real value. There is nowhere for them to look it up, so a BYON
// deploy could not be completed from the panel alone.
//
// Both are resolved to literal IPs on purpose. Core reaches Redis through a
// Swarm service name; a warp spoke has no DNS into the cluster, so copying
// "redis:6379" into a customer's compose file produces a node that starts,
// never resolves, and reports nothing.

// DeployAddrs are the overlay addresses that go into a BYON or route-only
// deploy snippet. An empty field means "could not be determined here" and the
// panel keeps showing its placeholder rather than a plausible wrong value.
type DeployAddrs struct {
	CoreGRPCAddr  string `json:"coreGrpcAddr"`
	RedisAddr     string `json:"redisAddr"`
	TunnelSubnets string `json:"tunnelSubnets"`
}

// overlayRedisAddr resolves Core's own REDIS_ADDR to host:port with a literal
// private IPv4 host. Returns "" when it cannot - which is the honest answer
// from a Core that reaches Redis some other way, and leaves the snippet's
// placeholder in place.
func overlayRedisAddr(redisEnv string) string {
	return resolveOverlayRedisAddr(redisEnv, net.LookupIP)
}

// resolveOverlayRedisAddr is overlayRedisAddr with the resolver injected, so
// the behaviour can be pinned without depending on what DNS a machine happens
// to have. That is not hypothetical: CI runs behind a resolver that answers
// NXDOMAIN with a public address of its own.
//
// Which is also why the result must be PRIVATE. This value is copied verbatim
// into a customer's compose file, and Core reaches Redis over an overlay, which
// is RFC1918 by construction. A public answer therefore did not name the
// overlay - it is a hijacked lookup or a misconfiguration - and putting a
// stranger's address in front of a customer is worse than a placeholder.
// Loopback is rejected for the same reason: it is never reachable from the
// machine that will read this.
func resolveOverlayRedisAddr(redisEnv string, lookup func(string) ([]net.IP, error)) string {
	redisEnv = strings.TrimSpace(redisEnv)
	if redisEnv == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(redisEnv)
	if err != nil {
		host, port = redisEnv, "6379"
	}

	usable := func(ip net.IP) string {
		v4 := ip.To4()
		if v4 == nil || !v4.IsPrivate() {
			return ""
		}
		return net.JoinHostPort(v4.String(), port)
	}

	if ip := net.ParseIP(host); ip != nil {
		return usable(ip)
	}
	ips, err := lookup(host)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if addr := usable(ip); addr != "" {
			return addr
		}
	}
	return ""
}

// deployAddrs answers with Core's overlay-side gRPC address, Redis, and the
// stored tunnel subnets.
//
// The gRPC host is the local address the kernel picks to reach Redis, NOT the
// first private IP: Core sits in several networks at once (the default bridge,
// the ingress overlay, a reverse-proxy overlay, the service overlay) and only
// the Redis-facing one is provably the overlay the spoke will be on.
func (s *AppState) deployAddrs(grpcPort string) DeployAddrs {
	redisEnv := os.Getenv("REDIS_ADDR")

	out := DeployAddrs{RedisAddr: overlayRedisAddr(redisEnv)}
	if local := localAddrToward(redisEnv); local != nil {
		out.CoreGRPCAddr = net.JoinHostPort(local.String(), grpcPort)
	}
	if s != nil && s.Store != nil {
		v, _ := s.Store.GetSetting("warp_tunnel_subnets")
		out.TunnelSubnets = strings.TrimSpace(v)
	}
	return out
}

// GetDeployAddrs GET /api/warp/deploy-config - any authenticated user.
//
// Deliberately not admin-only: the tenant who mints a BYON key on /nodes is the
// one who needs these values, and withholding them would only mean handing them
// over by email instead. They are RFC1918 addresses inside an overlay nobody
// reaches without their own authenticated warp key, so they authorize nothing on
// their own.
func (h *WarpHandler) GetDeployAddrs(w http.ResponseWriter, r *http.Request) {
	grpcPort := strings.TrimSpace(os.Getenv("DYLARIS_GRPC_PORT"))
	if grpcPort == "" {
		grpcPort = "25501"
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"addrs":   h.state.deployAddrs(grpcPort),
	})
}
