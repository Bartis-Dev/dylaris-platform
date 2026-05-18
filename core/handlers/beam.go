package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	beamauth "dylaris-pkg/beam/auth"
	"github.com/redis/go-redis/v9"
)

// Beam relay discovery — relays register themselves in Redis sorted-set
// "beam:relays" with score = unix-expiry. Entries past TTL are pruned on read.
const beamRelaysKey = "beam:relays"

// DiscoverBeamRelays returns the list of currently registered Beam relays,
// pruning any whose registration has expired.
func DiscoverBeamRelays(ctx context.Context, rdb *redis.Client) []string {
	if rdb == nil {
		return nil
	}
	now := time.Now().Unix()
	// Prune expired entries (score < now)
	rdb.ZRemRangeByScore(ctx, beamRelaysKey, "-inf", fmt.Sprintf("%d", now-1))
	res, err := rdb.ZRange(ctx, beamRelaysKey, 0, -1).Result()
	if err != nil {
		return nil
	}
	return res
}

// PickBeamRelay returns one relay address using random load-balancing,
// or empty string when none are registered.
func PickBeamRelay(ctx context.Context, rdb *redis.Client) string {
	relays := DiscoverBeamRelays(ctx, rdb)
	if len(relays) == 0 {
		return ""
	}
	return relays[rand.Intn(len(relays))]
}

// resolveRelay returns (effectiveAddress, source). Source is "discovered" or
// "manual" or empty. Manual override (DB setting beam.relay_address) wins so
// admins can force a specific relay during incidents.
func resolveRelay(ctx context.Context, rdb *redis.Client, manualOverride string) (string, string) {
	if strings.TrimSpace(manualOverride) != "" {
		return manualOverride, "manual"
	}
	if relay := PickBeamRelay(ctx, rdb); relay != "" {
		return relay, "discovered"
	}
	return "", ""
}

type BeamHandler struct {
	state     *AppState
	jwtSecret string
}

func NewBeamHandler(state *AppState, jwtSecret string) *BeamHandler {
	return &BeamHandler{state: state, jwtSecret: jwtSecret}
}

type BeamServerInfo struct {
	ID              int    `json:"id"`
	UUID            string `json:"uuid"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	NodeID          string `json:"node_id"`
	NodeName        string `json:"node_name"`
	ActiveSubServer string `json:"active_sub_server"`
}

// GetBeamServers returns the server list with node_id for Beam clients.
// GET /api/beam/servers
func (h *BeamHandler) GetBeamServers(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)

	// Use ListServersForUser which handles access control
	user, err := h.state.Store.GetUserByUsername(username)
	if err != nil || user == nil {
		sendJSONError(w, "User not found", http.StatusUnauthorized)
		return
	}

	servers, err := h.state.Store.ListServersForUser(user.ID, isAdmin)
	if err != nil {
		sendJSONError(w, "Failed to load servers", http.StatusInternalServerError)
		return
	}

	var result []BeamServerInfo
	for _, s := range servers {
		// Resolve node info — Node.Token is used as the discovery nodeID
		nodeDiscoveryID := ""
		nodeName := s.NodeName
		if s.NodeID > 0 {
			node, err := h.state.Store.GetNodeByID(s.NodeID)
			if err == nil && node != nil {
				nodeDiscoveryID = node.Token // Token = DYLARIS_NODE_ID used in discovery
				nodeName = node.Name
			}
		}

		result = append(result, BeamServerInfo{
			ID:              s.ID,
			UUID:            s.UUID,
			Name:            s.Name,
			Status:          s.Status,
			NodeID:          nodeDiscoveryID,
			NodeName:        nodeName,
			ActiveSubServer: s.ActiveSubServer,
		})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"servers": result,
	})
}

// GetBeamTicket signs a JWT ticket for a specific server.
// POST /api/beam/ticket
func (h *BeamHandler) GetBeamTicket(w http.ResponseWriter, r *http.Request) {
	username := r.Context().Value("username").(string)
	isAdmin := r.Context().Value("isAdmin").(bool)

	var req struct {
		ServerUUID string `json:"server_uuid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.ServerUUID == "" {
		sendJSONError(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	// Resolve server
	server, err := h.state.Store.GetServerByUUID(req.ServerUUID)
	if err != nil || server == nil {
		sendJSONError(w, "Server not found", http.StatusNotFound)
		return
	}

	// Check access (admin or owner or invited member)
	if !isAdmin {
		user, _ := h.state.Store.GetUserByUsername(username)
		if user == nil {
			sendJSONError(w, "User not found", http.StatusUnauthorized)
			return
		}
		hasAccess := server.OwnerID == user.ID
		if !hasAccess {
			invite, _ := h.state.Store.GetInvite(server.ID, user.ID)
			hasAccess = invite != nil
		}
		if !hasAccess {
			sendJSONError(w, "Access denied", http.StatusForbidden)
			return
		}
	}

	// Resolve node discovery ID (Token field = DYLARIS_NODE_ID)
	nodeDiscoveryID := ""
	if server.NodeID > 0 {
		node, err := h.state.Store.GetNodeByID(server.NodeID)
		if err == nil && node != nil {
			nodeDiscoveryID = node.Token
		}
	}
	if nodeDiscoveryID == "" {
		sendJSONError(w, "Server has no assigned node", http.StatusBadRequest)
		return
	}

	// Sign ticket via shared auth package — same format used by gateway
	// beam-relay validators and node-side BeamNodeService.Authenticate.
	claims := beamauth.BeamClaims{
		ServerUUID: server.UUID,
		NodeID:     nodeDiscoveryID,
		Username:   username,
		IsAdmin:    isAdmin,
	}
	ticketString, err := beamauth.SignBeamTicket(h.jwtSecret, claims)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Failed to sign ticket: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"ticket":  ticketString,
		"expires": claims.ExpiresAt.Time.Unix(),
	})
}

// GetBeamConfig returns the Beam relay address and branding info.
// GET /api/beam/config
func (h *BeamHandler) GetBeamConfig(w http.ResponseWriter, r *http.Request) {
	getSetting := func(key string) string {
		val, _ := h.state.Store.GetSetting(key)
		return val
	}

	manualOverride := getSetting("beam.relay_address")
	relayAddress, _ := resolveRelay(r.Context(), h.state.Redis, manualOverride)
	enabled := getSetting("beam.enabled")
	if enabled == "" {
		enabled = "true"
	}

	// Branding
	brandName := getSetting("branding.name")
	if brandName == "" {
		brandName = "Dylaris"
	}
	brandLogoURL := getSetting("branding.logo_url")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"relay_address": relayAddress,
		"enabled":       enabled == "true",
		"branding": map[string]string{
			"name":     brandName,
			"logo_url": brandLogoURL,
		},
	})
}

// GetBeamDownload redirects to a Beam binary on a load-balanced relay.
// GET /api/beam/download/{platform}
// platform ∈ {win, mac-arm, mac-intel, linux}
func (h *BeamHandler) GetBeamDownload(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	if platform == "" {
		// Allow path-style /api/beam/download (no platform) — return JSON list.
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"platforms": []string{"win", "mac-arm", "mac-intel", "linux"},
		})
		return
	}

	validPlatforms := map[string]bool{"win": true, "mac-arm": true, "mac-intel": true, "linux": true}
	if !validPlatforms[platform] {
		sendJSONError(w, "Invalid platform", http.StatusBadRequest)
		return
	}

	getSetting := func(key string) string {
		val, _ := h.state.Store.GetSetting(key)
		return val
	}

	// Manual override for the entire download URL still wins — useful when
	// shipping a binary from a separate CDN rather than via a relay.
	manualLink := getSetting("beam.download_link")
	if strings.TrimSpace(manualLink) != "" {
		http.Redirect(w, r, manualLink, http.StatusFound)
		return
	}

	relay, _ := resolveRelay(r.Context(), h.state.Redis, getSetting("beam.relay_address"))
	if relay == "" {
		sendJSONError(w, "No Beam relay available", http.StatusServiceUnavailable)
		return
	}

	// Relays expose /download/{platform} over HTTPS on their public address.
	scheme := "https"
	if strings.HasPrefix(relay, "http://") || strings.HasPrefix(relay, "https://") {
		http.Redirect(w, r, strings.TrimRight(relay, "/")+"/download/"+platform, http.StatusFound)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("%s://%s/download/%s", scheme, relay, platform), http.StatusFound)
}
