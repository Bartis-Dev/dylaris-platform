package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"dylaris-core/store"
	"dylaris-pkg/errlog"

	"github.com/redis/go-redis/v9"
)

const hubQueueKey = "dylaris:hub:queue"

// --- Redis-side types for reading gateway state ---

// GatewayEdgeInfo represents an Edge as stored in Redis (edge:registry:{id}).
// Stats are not part of the registry payload — they're published separately to
// the dylaris:edge:{id}:stats stream by the Edge service every few seconds and
// merged in by GetEdgesFromRedis.
type GatewayEdgeInfo struct {
	EdgeID      string         `json:"edge_id"`
	Name        string         `json:"name"`
	IP          string         `json:"ip"`
	PrivateIP   string         `json:"private_ip"`
	ServicePort string         `json:"service_port"`
	SplicePort  string         `json:"splice_port"`
	Status      string         `json:"status"`
	// Region + Wildcard are advertised by regional edges for the DNS updater.
	// Region groups edges (eu/us); Wildcard is the A-record name the reconciler
	// points at this region's edge IPs (e.g. "*.eu.dylaris.com"). Empty on edges
	// not configured for multi-region DNS.
	Region   string         `json:"region,omitempty"`
	Wildcard string         `json:"wildcard,omitempty"`
	Stats    *EdgeLiveStats `json:"stats,omitempty"`
}

// EdgeLiveStats mirrors the EdgeMetrics payload published by the Edge service
// to dylaris:edge:{id}:stats. Field names match the JSON the Edge produces.
type EdgeLiveStats struct {
	CPU                 float64 `json:"cpu"`
	RAMUsed             uint64  `json:"ram_used"`
	RAMTotal            uint64  `json:"ram_total"`
	RAMPercent          float64 `json:"ram_pct"`
	RxSpeed             uint64  `json:"rx_speed"`
	TxSpeed             uint64  `json:"tx_speed"`
	ActiveTunnels       int     `json:"active_tunnels"`
	ActiveTokens        int     `json:"active_tokens"`
	ActiveMCStreams     int64   `json:"active_mc_streams"`
	XDPEnabled          bool    `json:"xdp_enabled"`
	XDPPassed           uint64  `json:"xdp_passed"`
	XDPDroppedBlocked   uint64  `json:"xdp_dropped_blocked"`
	XDPDroppedRateLimit uint64  `json:"xdp_dropped_ratelimit"`
	XDPBlockedIPs       int     `json:"xdp_blocked_ips"`
}

// GatewayLinkStatus represents a link's online state as readable from Redis.
type GatewayLinkStatus struct {
	Token         string `json:"token"`
	Online        bool   `json:"online"`
	ActiveTunnels int    `json:"active_tunnels"`
}

// GatewayRoute represents a route as stored in Redis (route:{domain}).
type GatewayRoute struct {
	Domain     string `json:"domain"`
	TargetIP   string `json:"target_ip"`
	TargetPort int    `json:"target_port"`
	TunnelID   string `json:"tunnel_id"` // link token
	ServerUUID string `json:"server_uuid"`
}

// hubQueueMessage is the payload pushed to dylaris:hub:queue.
type hubQueueMessage struct {
	Action       string `json:"action"`
	Domain       string `json:"domain,omitempty"`
	TargetIP     string `json:"target_ip,omitempty"`
	TargetPort   int    `json:"target_port,omitempty"`
	LinkToken    string `json:"link_token,omitempty"`
	ServerUUID   string `json:"server_uuid,omitempty"`
	ServerID     *uint   `json:"server_id,omitempty"`
	OwnerID      *string `json:"owner_id,omitempty"`
	NewLinkToken string  `json:"new_link_token,omitempty"`
}

// --- GatewayProvider interface ---

// GatewayProvider handles route lifecycle operations (write path only).
// Reads are done directly from Redis using the helper functions below.
type GatewayProvider interface {
	CreateServerRoute(serverID uint, ownerID string, domain string, port int) error
	DeleteRoute(domain string) error
	MigrateServerRoutes(serverID uint, newNodeID uint) error
}

// --- RedisGateway (active in gateway / both routing modes) ---

type RedisGateway struct {
	redis         *redis.Client
	store         store.Store
	clusterSecret string
}

func NewRedisGateway(r *redis.Client, s store.Store, secret string) *RedisGateway {
	return &RedisGateway{redis: r, store: s, clusterSecret: secret}
}

func (g *RedisGateway) CreateServerRoute(serverID uint, ownerID string, domain string, port int) error {
	// 1. Resolve server
	server, err := g.store.GetServerByID(int(serverID))
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}

	// 2. Resolve node → derive link token
	node, err := g.store.GetNodeByID(server.NodeID)
	if err != nil {
		return fmt.Errorf("node not found: %w", err)
	}
	linkToken := DeriveLinkToken(node.Token, g.clusterSecret)

	// 3. Port enable checks (limit counts skipped — Hub enforces uniqueness in its DB)
	if port == 25565 {
		if val, _ := g.store.GetSetting("gateway_port_mc_enabled"); val == "false" {
			return fmt.Errorf("minecraft port (25565) routing is disabled")
		}
	}
	if port == 443 {
		if val, _ := g.store.GetSetting("gateway_port_https_enabled"); val == "false" {
			return fmt.Errorf("HTTPS port (443) routing is disabled")
		}
	}

	// 4. Push to queue
	sID := uint(serverID)
	oID := ownerID
	msg := hubQueueMessage{
		Action:     "create_route",
		Domain:     domain,
		TargetIP:   fmt.Sprintf("mc_%s", server.UUID),
		TargetPort: port,
		LinkToken:  linkToken,
		ServerUUID: server.UUID,
		ServerID:   &sID,
		OwnerID:    &oID,
	}

	return g.pushToQueue(msg)
}

func (g *RedisGateway) DeleteRoute(domain string) error {
	return g.pushToQueue(hubQueueMessage{
		Action: "delete_route",
		Domain: domain,
	})
}

func (g *RedisGateway) MigrateServerRoutes(serverID uint, newNodeID uint) error {
	server, err := g.store.GetServerByID(int(serverID))
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}

	node, err := g.store.GetNodeByID(int(newNodeID))
	if err != nil {
		return fmt.Errorf("node not found: %w", err)
	}
	newToken := DeriveLinkToken(node.Token, g.clusterSecret)

	return g.pushToQueue(hubQueueMessage{
		Action:       "migrate_routes",
		ServerUUID:   server.UUID,
		NewLinkToken: newToken,
	})
}

func (g *RedisGateway) pushToQueue(msg hubQueueMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return g.redis.RPush(ctx, hubQueueKey, string(data)).Err()
}

// DeriveLinkToken computes SHA256(nodeID + clusterSecret) — same as Link and Hub binaries.
func DeriveLinkToken(nodeID, clusterSecret string) string {
	h := sha256.New()
	h.Write([]byte(nodeID + clusterSecret))
	return hex.EncodeToString(h.Sum(nil))
}

// --- Redis read helpers ---

// GetEdgesFromRedis reads all edges from Redis (edge:registry:{id} keys) and
// merges the latest stats snapshot from each edge's stats stream.
func GetEdgesFromRedis(ctx context.Context, rdb *redis.Client) []GatewayEdgeInfo {
	var edges []GatewayEdgeInfo
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "edge:registry:*", 100).Result()
		if err != nil {
			break
		}
		for _, key := range keys {
			val, err := rdb.Get(ctx, key).Result()
			if err != nil {
				continue
			}
			var e GatewayEdgeInfo
			if json.Unmarshal([]byte(val), &e) == nil {
				if e.Status == "" {
					e.Status = "online"
				}
				e.Stats = readLatestEdgeStats(ctx, rdb, e.EdgeID)
				edges = append(edges, e)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return edges
}

// readLatestEdgeStats fetches the newest entry from the per-edge stats stream
// written by the Edge service's metrics collector. Returns nil if the stream
// is empty, missing, or unreadable — never blocks the overview response.
func readLatestEdgeStats(ctx context.Context, rdb *redis.Client, edgeID string) *EdgeLiveStats {
	if edgeID == "" {
		return nil
	}
	streamKey := "dylaris:edge:" + edgeID + ":stats"
	msgs, err := rdb.XRevRangeN(ctx, streamKey, "+", "-", 1).Result()
	if err != nil || len(msgs) == 0 {
		return nil
	}
	raw, ok := msgs[0].Values["data"].(string)
	if !ok || raw == "" {
		return nil
	}
	var s EdgeLiveStats
	if json.Unmarshal([]byte(raw), &s) != nil {
		return nil
	}
	return &s
}

// GetLinksFromRedis reads all known link tokens and their online status from Redis.
func GetLinksFromRedis(ctx context.Context, rdb *redis.Client) []GatewayLinkStatus {
	// Tokens with active keep-alive — one self-expiring key per live link
	// (online_link:<token>, 15s TTL). A dead link's key expires on its own,
	// unlike the old shared set whose single TTL any live link refreshed.
	onlineMap := make(map[string]bool)
	var oCursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, oCursor, "online_link:*", 100).Result()
		if err != nil {
			break
		}
		for _, key := range keys {
			onlineMap[key[len("online_link:"):]] = true
		}
		oCursor = next
		if oCursor == 0 {
			break
		}
	}

	// All known tokens from link:{token} keys
	var links []GatewayLinkStatus
	var cursor uint64
	seen := make(map[string]bool)
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "link:*", 100).Result()
		if err != nil {
			break
		}
		for _, key := range keys {
			token := key[5:] // strip "link:"
			if seen[token] {
				continue
			}
			seen[token] = true

			tunnels := 0
			stats, _ := rdb.HGetAll(ctx, "sys:stats:tunnels:"+token).Result()
			for _, v := range stats {
				if n, err := strconv.Atoi(v); err == nil {
					tunnels += n
				}
			}
			links = append(links, GatewayLinkStatus{
				Token:         token,
				Online:        onlineMap[token] || tunnels > 0,
				ActiveTunnels: tunnels,
			})
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return links
}

// GetRoutesFromRedis reads all routes from Redis (sys:index:routes + route:{domain}).
func GetRoutesFromRedis(ctx context.Context, rdb *redis.Client) []GatewayRoute {
	domains, err := rdb.SMembers(ctx, "sys:index:routes").Result()
	if err != nil {
		return nil
	}
	routes := make([]GatewayRoute, 0, len(domains))
	for _, domain := range domains {
		val, err := rdb.Get(ctx, "route:"+domain).Result()
		if err != nil {
			continue
		}
		var r GatewayRoute
		if json.Unmarshal([]byte(val), &r) == nil {
			r.Domain = domain
			routes = append(routes, r)
		}
	}
	return routes
}

// CountRoutesFromRedis returns the total number of routes in Redis.
func CountRoutesFromRedis(ctx context.Context, rdb *redis.Client) int64 {
	count, _ := rdb.SCard(ctx, "sys:index:routes").Result()
	return count
}

// GetServiceErrorsFromRedis reads error log entries from Redis Streams.
func GetServiceErrorsFromRedis(rdb *redis.Client, service string, count int64) []errlog.Entry {
	streams, err := errlog.ScanErrorStreams(rdb, service)
	if err != nil {
		return nil
	}
	perStream := count
	if len(streams) > 1 {
		perStream = count / int64(len(streams))
		if perStream < 10 {
			perStream = 10
		}
	}
	var all []errlog.Entry
	for _, stream := range streams {
		entries, err := errlog.ReadEntries(rdb, stream, perStream)
		if err != nil {
			continue
		}
		all = append(all, entries...)
	}
	return all
}

// GetAllServiceErrorsFromRedis reads errors for all gateway service types.
func GetAllServiceErrorsFromRedis(rdb *redis.Client, count int64) map[string][]errlog.Entry {
	result := make(map[string][]errlog.Entry)
	for _, svc := range []string{"gate", "link"} {
		entries := GetServiceErrorsFromRedis(rdb, svc, count)
		if len(entries) > 0 {
			result[svc] = entries
		}
	}
	return result
}
