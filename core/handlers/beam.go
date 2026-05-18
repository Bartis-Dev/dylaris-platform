package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"

	beamauth "dylaris-pkg/beam/auth"
	"github.com/redis/go-redis/v9"
)

// Beam relay discovery — matches the heartbeat pattern in
// gateway/beam/relay: each relay writes JSON to beam:registry:<id> with a
// 30s TTL and adds its id to the sys:beams set. The Core resolves "alive"
// relays by intersecting the set with surviving keys.
const beamRegistrySet = "sys:beams"

// BeamRelayInfo describes one discovered relay. Mirrors what the relay's
// heartbeat publishes so we can surface it raw in the admin UI.
type BeamRelayInfo struct {
	BeamID       string `json:"beam_id"`
	IP           string `json:"ip"`
	PrivateIP    string `json:"private_ip,omitempty"`
	ServicePort  string `json:"service_port"`
	ClientPort   string `json:"client_port,omitempty"`
	DownloadPort string `json:"download_port,omitempty"`
	Timestamp    int64  `json:"timestamp"`
}

// DiscoverBeamRelays reads sys:beams and resolves each entry against the
// corresponding beam:registry:<id> key. Stale entries (TTL expired) are
// skipped silently — the relay's own cleanup loop will eventually prune
// them from sys:beams.
func DiscoverBeamRelays(ctx context.Context, rdb *redis.Client) []BeamRelayInfo {
	if rdb == nil {
		return nil
	}
	ids, err := rdb.SMembers(ctx, beamRegistrySet).Result()
	if err != nil {
		return nil
	}
	out := make([]BeamRelayInfo, 0, len(ids))
	for _, id := range ids {
		raw, err := rdb.Get(ctx, "beam:registry:"+id).Bytes()
		if err != nil {
			continue
		}
		var info BeamRelayInfo
		if json.Unmarshal(raw, &info) != nil {
			continue
		}
		if info.BeamID == "" {
			info.BeamID = id
		}
		out = append(out, info)
	}
	return out
}

// PickBeamRelay returns one relay using simple random load-balancing.
// Returns the zero value when none are registered.
func PickBeamRelay(ctx context.Context, rdb *redis.Client) (BeamRelayInfo, bool) {
	relays := DiscoverBeamRelays(ctx, rdb)
	if len(relays) == 0 {
		return BeamRelayInfo{}, false
	}
	return relays[rand.Intn(len(relays))], true
}

// resolveRelay returns the effective relay address for a client. The address
// is the relay's public IP:client_port (TLS service for Beam Desktop) since
// that's what the panel surfaces in the Files tab. Manual override (DB
// setting beam.relay_address) wins for incident overrides.
func resolveRelay(ctx context.Context, rdb *redis.Client, manualOverride string) (string, string) {
	if strings.TrimSpace(manualOverride) != "" {
		return manualOverride, "manual"
	}
	if info, ok := PickBeamRelay(ctx, rdb); ok {
		port := info.ClientPort
		if port == "" {
			port = info.ServicePort
		}
		host := info.IP
		if host == "" {
			host = info.BeamID
		}
		if host != "" && port != "" {
			return host + ":" + port, "discovered"
		}
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

// validBeamPlatforms mirrors gateway/beam/relay/binaries.go validPlatforms.
// Keep in sync — adding a new platform requires both ends.
var validBeamPlatforms = map[string]bool{
	"windows-amd64": true,
	"linux-amd64":   true,
	"linux-arm64":   true,
	"darwin-amd64":  true,
	"darwin-arm64":  true,
}

// GetBeamDownload redirects to a Beam binary on a load-balanced relay.
// GET /api/beam/download?platform={os}-{arch}
// Returns the platform index when no platform query is given.
func (h *BeamHandler) GetBeamDownload(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	if platform == "" {
		platforms := make([]string, 0, len(validBeamPlatforms))
		for p := range validBeamPlatforms {
			platforms = append(platforms, p)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"platforms": platforms,
		})
		return
	}

	if !validBeamPlatforms[platform] {
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

	// Manual override for the relay host:port — keeps the override semantic
	// of phase 1 working, but downloads still route through the relay's
	// /download/* HTTP endpoint.
	if override := strings.TrimSpace(getSetting("beam.relay_address")); override != "" {
		http.Redirect(w, r, fmt.Sprintf("https://%s/download/%s", override, platform), http.StatusFound)
		return
	}

	info, ok := PickBeamRelay(r.Context(), h.state.Redis)
	if !ok {
		sendJSONError(w, "No Beam relay available", http.StatusServiceUnavailable)
		return
	}
	port := info.DownloadPort
	if port == "" {
		port = "25552" // gateway/beam/relay BEAM_DOWNLOAD_PORT default
	}
	host := info.IP
	if host == "" {
		host = info.BeamID
	}
	http.Redirect(w, r, fmt.Sprintf("https://%s:%s/download/%s", host, port, platform), http.StatusFound)
}
