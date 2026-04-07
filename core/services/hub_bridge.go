package services

import (
	"context"
	"fmt"
	"log"

	"dylaris-core/store"

	"dylaris-hub/pkg/hub"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// HubBridge wraps Hub's HubService and provides gateway operations for Core.
// Hub manages all gateway DB operations via GORM. Core adds server/user/limit logic on top.
type HubBridge struct {
	Hub   *hub.HubService
	Store store.Store // Core's raw SQL store for non-gateway queries (servers, nodes, users, route_limits)
}

// NewHubBridge creates a HubBridge that integrates Hub's library into Core.
func NewHubBridge(s store.Store, gormDB *gorm.DB, redisClient *redis.Client, clusterSecret string) *HubBridge {
	hubSvc := hub.New(gormDB, redisClient, hub.Config{
		ClusterSecret: clusterSecret,
		TablePrefix:   "gateway_",
	})

	return &HubBridge{
		Hub:   hubSvc,
		Store: s,
	}
}

// Start begins Hub's background tasks (sync, gate health check, link discovery).
func (b *HubBridge) Start() {
	b.Hub.StartBackgroundTasks()
	log.Println("HubBridge started (Gateway sync, gate health, link discovery)")
}

// Stop gracefully shuts down Hub's background tasks.
func (b *HubBridge) Stop() {
	b.Hub.Stop()
}

// --- ROUTE OPERATIONS (combines Hub + Core's limit logic) ---

// CreateServerRoute creates a route for a server, resolving node→IP+link and checking limits.
func (b *HubBridge) CreateServerRoute(serverID, ownerID int, domain string, targetPort int) (*hub.Route, error) {
	// 1. Resolve server → node → IP + link
	server, err := b.Store.GetServerByID(serverID)
	if err != nil {
		return nil, fmt.Errorf("server not found: %w", err)
	}

	node, err := b.Store.GetNodeByID(server.NodeID)
	if err != nil {
		return nil, fmt.Errorf("node not found: %w", err)
	}

	// Target is the MC container name on dylaris_net (Docker DNS resolves it)
	targetIP := fmt.Sprintf("mc_%s", server.UUID)

	// Find the gateway link for this node (node.Token = DYLARIS_NODE_ID used by Link discovery)
	link, err := b.Hub.GetLinkByNodeID(node.Token)
	if err != nil {
		return nil, fmt.Errorf("no gateway link found for node %s: %w", node.Token, err)
	}

	// 2. Check route limits
	if err := b.CheckRouteLimit(ownerID, serverID, targetPort); err != nil {
		return nil, err
	}

	// 3. Create route via Hub
	serverIDUint := uint(serverID)
	ownerIDUint := uint(ownerID)
	route := hub.Route{
		Domain:     domain,
		TargetIP:   targetIP,
		TargetPort: targetPort,
		LinkID:     link.ID,
		ServerUUID: server.UUID,
		ServerID:   &serverIDUint,
		OwnerID:    &ownerIDUint,
	}

	created, err := b.Hub.CreateRoute(route)
	if err != nil {
		return nil, err
	}

	b.Hub.Log("INFO", "GATEWAY", fmt.Sprintf("Route '%s' created for server %d by user %d", domain, serverID, ownerID))
	return created, nil
}

// CheckRouteLimit validates that a route creation doesn't exceed any configured limits.
func (b *HubBridge) CheckRouteLimit(ownerID, serverID, targetPort int) error {
	// 1. Port enable check
	if targetPort == 25565 {
		val, _ := b.Store.GetSetting("gateway_port_mc_enabled")
		if val == "false" {
			return fmt.Errorf("minecraft port (25565) routing is disabled")
		}
	}
	if targetPort == 443 {
		val, _ := b.Store.GetSetting("gateway_port_https_enabled")
		if val == "false" {
			return fmt.Errorf("HTTPS port (443) routing is disabled")
		}
	}

	// 2. Global limit
	globalLimit, err := b.Store.GetGatewayRouteLimit("global")
	if err == nil && globalLimit.MaxRoutes != -1 {
		total := b.Hub.CountRoutesTotal()
		if total >= int64(globalLimit.MaxRoutes) {
			return fmt.Errorf("global route limit reached (%d/%d)", total, globalLimit.MaxRoutes)
		}
	}

	// 3. Per-user limit
	userScope := fmt.Sprintf("user:%d", ownerID)
	userLimit, err := b.Store.GetGatewayRouteLimit(userScope)
	if err != nil {
		userLimit, err = b.Store.GetGatewayRouteLimit("user_default")
	}
	if err == nil && userLimit.MaxRoutes != -1 {
		count := b.Hub.CountRoutesByOwner(uint(ownerID))
		if count >= int64(userLimit.MaxRoutes) {
			return fmt.Errorf("user route limit reached (%d/%d)", count, userLimit.MaxRoutes)
		}
	}

	// 4. Per-server limit
	serverLimit, err := b.Store.GetGatewayRouteLimit("per_server")
	if err == nil && serverLimit.MaxRoutes != -1 {
		count := b.Hub.CountRoutesByServer(uint(serverID))
		if count >= int64(serverLimit.MaxRoutes) {
			return fmt.Errorf("per-server route limit reached (%d/%d)", count, serverLimit.MaxRoutes)
		}
	}

	// 5. Per-port limit
	portScope := fmt.Sprintf("port:%d", targetPort)
	portLimit, err := b.Store.GetGatewayRouteLimit(portScope)
	if err == nil && portLimit.MaxRoutes != -1 {
		count := b.Hub.CountRoutesByPort(targetPort)
		if count >= int64(portLimit.MaxRoutes) {
			return fmt.Errorf("per-port route limit reached (%d/%d)", count, portLimit.MaxRoutes)
		}
	}

	return nil
}

// MigrateServerRoutes updates all routes for a server to point to a new node.
// Called when a server is moved between nodes.
func (b *HubBridge) MigrateServerRoutes(serverID int, newNodeID int) error {
	server, err := b.Store.GetServerByID(serverID)
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}

	node, err := b.Store.GetNodeByID(newNodeID)
	if err != nil {
		return fmt.Errorf("node not found: %w", err)
	}

	// Target is the MC container name on dylaris_net (Docker DNS resolves it)
	targetIP := fmt.Sprintf("mc_%s", server.UUID)

	// node.Token = DYLARIS_NODE_ID used by Link discovery
	link, err := b.Hub.GetLinkByNodeID(node.Token)
	if err != nil {
		return fmt.Errorf("no gateway link for node %s: %w", node.Token, err)
	}

	return b.Hub.MigrateServerRoutes(uint(serverID), targetIP, link.ID)
}

// SyncData triggers an immediate Redis sync.
func (b *HubBridge) SyncData() {
	b.Hub.SyncData(context.Background())
}
