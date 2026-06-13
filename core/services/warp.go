package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/hkdf"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"dylaris-core/store"
)

// warpStore is the narrow store surface Enroll needs (satisfied by store.Store
// and by test fakes).
type warpStore interface {
	GetWarpPeerByPubkey(pubkey string) (*store.WarpPeer, error)
	ListAllWarpPeers() ([]store.WarpPeer, error)
	EnrollPeerTx(keyID, limit int, onNewConn, pubkey, fixedIP, leaderID string, allocIP func(taken map[string]bool) (string, error)) (wgIP string, evicted string, err error)
}

// WarpService owns the warp peer registry side of Core: it derives the leader
// public key, allocates WG IPs, enforces connection policies, and pushes peer
// commands to the leader's Redis queue.
type WarpService struct {
	warp          warpStore
	redis         *redis.Client
	clientSubnet  string // e.g. "10.0.99.0/24"
	leaderID      string // "leader-01"
	clusterSecret string // for deriving the leader's public key (no heartbeat)
}

func NewWarpService(s warpStore, r *redis.Client, clientSubnet, leaderID, clusterSecret string) *WarpService {
	return &WarpService{warp: s, redis: r, clientSubnet: clientSubnet, leaderID: leaderID, clusterSecret: clusterSecret}
}

// LeaderID exposes the configured leader id.
func (s *WarpService) LeaderID() string { return s.leaderID }

// LeaderPublicKey derives the leader's WG public key from clusterSecret+leaderID.
func (s *WarpService) LeaderPublicKey() (string, error) {
	return DeriveLeaderPublicKey(s.clusterSecret, s.leaderID)
}

// leaderKeyHKDFSalt is the fixed domain-separation salt for the leader-key
// derivation. MUST stay byte-identical to gateway/warp/keys.go.
const leaderKeyHKDFSalt = "dylaris/warp/leader-key/v1"

// DeriveLeaderPublicKey reproduces the gateway warp DeriveLeaderKey derivation
// so Core knows the leader's pubkey without a heartbeat. It expands the cluster
// secret with HKDF-SHA256 (fixed salt + leader ID as info). MUST stay
// byte-identical to gateway/warp/keys.go DeriveLeaderKey.
func DeriveLeaderPublicKey(clusterSecret, leaderID string) (string, error) {
	var km [32]byte
	r := hkdf.New(sha256.New, []byte(clusterSecret), []byte(leaderKeyHKDFSalt), []byte(leaderID))
	if _, err := io.ReadFull(r, km[:]); err != nil {
		return "", fmt.Errorf("hkdf expand: %w", err)
	}
	k, err := wgtypes.NewKey(km[:])
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

// EnrollResult mirrors the gateway client's enrollResponse JSON.
type EnrollResult struct {
	WGIP            string `json:"wg_ip"`
	WGSubnet        string `json:"wg_subnet"`
	LeaderPublicKey string `json:"leader_public_key"`
	LeaderEndpoint  string `json:"leader_endpoint"`
	DNS             string `json:"dns,omitempty"`
	Keepalive       int    `json:"keepalive"`
}

func (s *WarpService) leaderQueueKey() string { return "dylaris:warp:" + s.leaderID + ":queue" }

func (s *WarpService) pushCommand(ctx context.Context, cmd map[string]interface{}) error {
	b, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	return s.redis.RPush(ctx, s.leaderQueueKey(), b).Err()
}

// Enroll enforces the key's policy, allocates/persists a peer, pushes add_peer
// (plus remove_peer for kill_old), and returns the tunnel config (WGIP/WGSubnet;
// the handler fills leader pubkey/endpoint).
func (s *WarpService) Enroll(ctx context.Context, key store.WarpAPIKey, pubkey string, _ []string) (EnrollResult, error) {
	// Idempotent: same pubkey already enrolled → return its IP, no new push.
	if existing, err := s.warp.GetWarpPeerByPubkey(pubkey); err == nil && existing != nil {
		return EnrollResult{WGIP: existing.WGIP, WGSubnet: s.clientSubnet, Keepalive: 25}, nil
	}

	limit := key.MaxConns
	if key.Policy == "fixed" {
		limit = 1
	}

	// Atomic: policy enforcement + IP allocation + insert under one tx + advisory lock.
	wgIP, evicted, err := s.warp.EnrollPeerTx(key.ID, limit, key.OnNewConn, pubkey, key.FixedWGIP, s.leaderID,
		func(taken map[string]bool) (string, error) { return NextFreeIP(s.clientSubnet, taken) })
	if err != nil {
		if errors.Is(err, store.ErrWarpLimitReached) {
			// Preserve the sentinel (%w) so the handler can answer 409 for this
			// genuine conflict while treating other enroll errors as 500.
			return EnrollResult{}, fmt.Errorf("%w (policy=%s, max=%d)", store.ErrWarpLimitReached, key.Policy, limit)
		}
		return EnrollResult{}, err
	}

	// Push leader commands after the DB commit (best-effort; resync covers leader reboots).
	if evicted != "" {
		if perr := s.pushCommand(ctx, map[string]interface{}{"type": "remove_peer", "pubkey": evicted}); perr != nil {
			fmt.Printf("[warp] enroll: remove_peer push failed for evicted %s: %v\n", evicted, perr)
		}
	}
	if err := s.pushCommand(ctx, map[string]interface{}{"type": "add_peer", "pubkey": pubkey, "allowed_ips": []string{wgIP + "/32"}}); err != nil {
		return EnrollResult{}, err
	}

	return EnrollResult{WGIP: wgIP, WGSubnet: s.clientSubnet, Keepalive: 25}, nil
}

// processResyncRequest checks for a pending leader resync-request and, if set
// for our leader, pushes the full peer set as a `resync` command then clears it.
func (s *WarpService) processResyncRequest(ctx context.Context) error {
	leaderID, err := s.redis.Get(ctx, "dylaris:warp:resync-request").Result()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return err
	}
	if leaderID != s.leaderID {
		return nil
	}
	all, err := s.warp.ListAllWarpPeers()
	if err != nil {
		return err
	}
	peers := make([]map[string]interface{}, 0, len(all))
	for _, p := range all {
		peers = append(peers, map[string]interface{}{
			"pubkey": p.Pubkey, "wg_ip": p.WGIP, "allowed_ips": []string{p.WGIP + "/32"},
		})
	}
	if err := s.pushCommand(ctx, map[string]interface{}{"type": "resync", "peers": peers}); err != nil {
		return err
	}
	return s.redis.Del(ctx, "dylaris:warp:resync-request").Err()
}

// StartResyncWatcher runs processResyncRequest on a ticker. isLeader gates it so
// only the elected Core acts.
func (s *WarpService) StartResyncWatcher(isLeader func() bool) {
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
			if isLeader != nil && !isLeader() {
				continue
			}
			if err := s.processResyncRequest(context.Background()); err != nil {
				fmt.Printf("[warp] resync watcher: %v\n", err)
			}
		}
	}()
}
