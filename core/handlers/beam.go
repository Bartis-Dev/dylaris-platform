package handlers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

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
	PublicHost   string `json:"public_host,omitempty"` // e.g. "beam.dylaris.com" — preferred over IP
	ServicePort  string `json:"service_port"`
	ClientPort   string `json:"client_port,omitempty"`
	DownloadPort string `json:"download_port,omitempty"`
	Timestamp    int64  `json:"timestamp"`
}

// PublicAddress returns the host:port the panel should hand out to clients.
// Prefers the operator-configured public host over the internal overlay IP
// so we never accidentally publish a 172.x address to a browser.
func (i BeamRelayInfo) PublicAddress(port string) string {
	host := strings.TrimSpace(i.PublicHost)
	if host == "" {
		host = i.IP
	}
	if host == "" {
		host = i.BeamID
	}
	if port == "" || strings.Contains(host, ":") {
		return host
	}
	return host + ":" + port
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

// resolveRelay returns the effective public address for Beam Desktop
// clients. Prefers the relay's BEAM_PUBLIC_HOST (e.g. beam.dylaris.com)
// over the internal overlay IP — the IP is never reachable from a
// browser/desktop client and would produce the 172.x:25551 problem we saw
// in v1. Manual override (DB setting beam.relay_address) still wins for
// incident routing.
func resolveRelay(ctx context.Context, rdb *redis.Client, manualOverride string) (string, string) {
	if strings.TrimSpace(manualOverride) != "" {
		return manualOverride, "manual"
	}
	if info, ok := PickBeamRelay(ctx, rdb); ok {
		port := info.ClientPort
		if port == "" {
			port = info.ServicePort
		}
		addr := info.PublicAddress(port)
		if addr != "" {
			return addr, "discovered"
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

// downloadRelayClient is a tuned HTTP client that talks to relays over
// HTTPS. The relay listens on a self-signed cert by default (with a
// proper one configured via TLS_CERT_PATH only on production deploys),
// so we skip verification when the request is between Core and Relay
// over the internal overlay network. The byte stream is end-to-end via
// Core's own TLS to the browser, so the user-facing path stays trusted.
var downloadRelayClient = &http.Client{
	Timeout: 5 * time.Minute,
	Transport: &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       60 * time.Second,
	},
}

// GetBeamDownload streams a Beam binary through Core. We deliberately do
// NOT 302-redirect any more — that exposes the relay's internal IP /
// hostname to the browser, which usually means a 172.x address on the
// overlay network or a hostname with a self-signed cert. By reverse-
// proxying through Core the browser only ever sees a single trusted
// Core-served URL.
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

	// Resolve the relay we'll proxy from. We prefer hitting the relay on
	// its overlay address (`ip` from the heartbeat) so the request never
	// leaves Docker's internal network — there's no point in Core fetching
	// the binary from beam.dylaris.com over the public internet just to
	// hand it back to the user.
	upstream, err := resolveDownloadUpstream(r.Context(), h.state.Redis, h.state.Store, getSetting, platform)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
	if err != nil {
		sendJSONError(w, "Could not build upstream request", http.StatusInternalServerError)
		return
	}
	// Pass through the User-Agent so the relay's logs are useful, but drop
	// any client-supplied auth headers — the relay's /download endpoint is
	// public and we don't want to leak browser cookies.
	if ua := r.Header.Get("User-Agent"); ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	resp, err := downloadRelayClient.Do(req)
	if err != nil {
		sendJSONError(w, "Could not reach Beam relay: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		sendJSONError(w, fmt.Sprintf("Relay returned %d for %s", resp.StatusCode, platform), http.StatusBadGateway)
		return
	}

	// Mirror the relay's content headers so the browser saves the file
	// with the right filename and gets a real Content-Length progress bar.
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		w.Header().Set("Content-Disposition", cd)
	} else {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="beam-%s"`, platform))
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	w.WriteHeader(http.StatusOK)
	io.Copy(w, resp.Body)
}

// resolveDownloadUpstream picks the relay URL Core should pull from. Falls
// back through manual settings and finally onto the auto-discovered relay's
// overlay IP. Returns the fully-qualified https://host:port/download/...
func resolveDownloadUpstream(ctx context.Context, rdb *redis.Client, _ interface{}, getSetting func(string) string, platform string) (string, error) {
	// `beam.download_link` is a complete URL override — useful when an op
	// wants to point at a CDN-hosted mirror rather than the live relay.
	if link := strings.TrimSpace(getSetting("beam.download_link")); link != "" {
		base := strings.TrimRight(link, "/")
		// If the override already includes a /download/... path, treat it
		// as the literal mirror URL; otherwise tack on /download/{platform}.
		if strings.Contains(base, "/download/") {
			return base, nil
		}
		return base + "/download/" + platform, nil
	}

	// `beam.relay_address` is a single host:port override, used during
	// incidents to pin to one relay.
	if override := strings.TrimSpace(getSetting("beam.relay_address")); override != "" {
		// download port is fixed by convention but configurable per relay.
		// The override addresses the client TLS port, so look up the
		// matching download_port from any discovered relay.
		downloadPort := "25552"
		if relays := DiscoverBeamRelays(ctx, rdb); len(relays) > 0 && relays[0].DownloadPort != "" {
			downloadPort = relays[0].DownloadPort
		}
		host := override
		if idx := strings.IndexByte(host, ':'); idx >= 0 {
			host = host[:idx]
		}
		return fmt.Sprintf("https://%s:%s/download/%s", host, downloadPort, platform), nil
	}

	info, ok := PickBeamRelay(ctx, rdb)
	if !ok {
		return "", fmt.Errorf("no Beam relay available")
	}
	port := info.DownloadPort
	if port == "" {
		port = "25552"
	}
	// Prefer the internal IP for the Core→Relay hop. Inside Swarm the
	// container can route to the overlay address directly; the public
	// hostname only matters for the browser/desktop-app facing path.
	host := info.IP
	if host == "" {
		host = info.PublicHost
	}
	if host == "" {
		host = info.BeamID
	}
	if host == "" {
		return "", fmt.Errorf("relay has no reachable address")
	}
	return fmt.Sprintf("https://%s:%s/download/%s", host, port, platform), nil
}
