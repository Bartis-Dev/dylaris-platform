package services

import (
	"crypto/sha256"
	"fmt"
	"net"

	"github.com/redis/go-redis/v9"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"dylaris-core/store"
)

// WarpService owns the warp peer registry side of Core: it derives the leader
// public key, allocates WG IPs, enforces connection policies, and pushes peer
// commands to the leader's Redis queue.
type WarpService struct {
	store store.Store
	redis *redis.Client
}

func NewWarpService(s store.Store, r *redis.Client) *WarpService {
	return &WarpService{store: s, redis: r}
}

// DeriveLeaderPublicKey reproduces the gateway warp DeriveLeaderKey derivation
// so Core knows the leader's pubkey without a heartbeat. MUST stay byte-identical
// to gateway/warp/keys.go DeriveLeaderKey.
func DeriveLeaderPublicKey(clusterSecret, leaderID string) (string, error) {
	sum := sha256.Sum256([]byte("warp-leader:" + clusterSecret + ":" + leaderID))
	k, err := wgtypes.NewKey(sum[:])
	if err != nil {
		return "", fmt.Errorf("derive leader key: %w", err)
	}
	return k.PublicKey().String(), nil
}

// NextFreeIP returns the first usable host IP in subnet not in `taken`. The first
// host (.1 in a /24) is reserved for the leader. Network + broadcast are skipped.
func NextFreeIP(subnet string, taken map[string]bool) (string, error) {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("parse subnet %q: %w", subnet, err)
	}
	base := ipnet.IP.Mask(ipnet.Mask).To4()
	if base == nil {
		return "", fmt.Errorf("only IPv4 subnets supported: %q", subnet)
	}
	cur := make(net.IP, len(base))
	copy(cur, base)
	leaderReserved := true
	for {
		incIP(cur)
		if !ipnet.Contains(cur) {
			return "", fmt.Errorf("subnet %s exhausted", subnet)
		}
		if leaderReserved {
			leaderReserved = false
			continue // skip the leader-reserved first host (e.g. .1)
		}
		next := make(net.IP, len(cur))
		copy(next, cur)
		incIP(next)
		if !ipnet.Contains(next) {
			return "", fmt.Errorf("subnet %s exhausted", subnet) // cur is broadcast
		}
		if !taken[cur.String()] {
			return cur.String(), nil
		}
	}
}

func incIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}
