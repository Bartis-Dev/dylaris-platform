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

// defaultCoreServiceName is the name Core answers to inside the overlay. It is
// one of the four load-bearing service names in the production stack, so the
// default is right for every deployment that follows it; CORE_SERVICE_NAME
// covers the ones that do not.
const defaultCoreServiceName = "core"

// overlayRedisAddr resolves Core's own REDIS_ADDR to host:port with a literal
// private IPv4 host. Returns "" when it cannot - which is the honest answer
// from a Core that reaches Redis some other way, and leaves the snippet's
// placeholder in place.
func overlayRedisAddr(redisEnv string) string {
	return resolveOverlayRedisAddr(redisEnv, net.LookupIP)
}

func resolveOverlayRedisAddr(redisEnv string, lookup func(string) ([]net.IP, error)) string {
	return resolveOverlayAddr(redisEnv, "6379", lookup)
}

// overlayCoreAddr resolves Core's OWN service name, so the snippet names the
// service rather than the instance that happened to answer the request.
//
// Core's task IP - what this used to hand out - changes on every redeploy and
// names exactly one replica when Core is scaled, while the RedisAddr beside it
// has always been a resolved service name. The two were inconsistent and only
// one of them was stable.
func overlayCoreAddr(grpcPort string) string {
	name := strings.TrimSpace(os.Getenv("CORE_SERVICE_NAME"))
	if name == "" {
		name = defaultCoreServiceName
	}
	return resolveOverlayAddr(name, grpcPort, net.LookupIP)
}

// resolveOverlayAddr turns a service name or literal address into "ip:port" with
// a literal private IPv4 host, using defaultPort when the input carries none.
//
// The resolver is injected so the behaviour can be pinned without depending on
// what DNS a machine happens to have. That is not hypothetical: CI runs behind a
// resolver that answers NXDOMAIN with a public address of its own.
//
// Which is also why the result must be PRIVATE. This value is copied verbatim
// into a customer's compose file, and Core reaches both Redis and its own
// service over an overlay, which is RFC1918 by construction. A public answer
// therefore did not name the overlay - it is a hijacked lookup or a
// misconfiguration - and putting a stranger's address in front of a customer is
// worse than a placeholder. Loopback is rejected for the same reason: it is
// never reachable from the machine that will read this.
func resolveOverlayAddr(hostPort, defaultPort string, lookup func(string) ([]net.IP, error)) string {
	hostPort = strings.TrimSpace(hostPort)
	if hostPort == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		host, port = hostPort, defaultPort
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
// tunnel subnets.
func (s *AppState) deployAddrs(grpcPort string) DeployAddrs {
	redisEnv := os.Getenv("REDIS_ADDR")

	out := DeployAddrs{
		RedisAddr:    overlayRedisAddr(redisEnv),
		CoreGRPCAddr: overlayCoreAddr(grpcPort),
	}
	if out.CoreGRPCAddr == "" {
		// Fallback for a Core that does not answer to its own service name -
		// a single-container install, or a stack that renamed the service
		// without setting CORE_SERVICE_NAME. The local address the kernel picks
		// to reach Redis, NOT the first private IP: Core sits in several
		// networks at once (the default bridge, the ingress overlay, a
		// reverse-proxy overlay, the service overlay) and only the Redis-facing
		// one is provably the overlay the spoke will be on. It is an instance
		// address, so it is second choice, not first.
		if local := localAddrToward(redisEnv); local != nil {
			out.CoreGRPCAddr = net.JoinHostPort(local.String(), grpcPort)
		}
	}
	if s != nil && s.Store != nil {
		v, _ := s.Store.GetSetting("warp_tunnel_subnets")
		out.TunnelSubnets = strings.TrimSpace(v)
	}
	// Nothing stored: fall back to the value Core can detect about its own
	// networks. The snippet is only useful if every field in it is filled, and
	// an operator who never opened the warp settings has no stored value - which
	// is the normal case, not an edge one.
	if out.TunnelSubnets == "" && s != nil {
		out.TunnelSubnets = s.suggestTunnelSubnets().Suggested
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
