package handlers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
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
// clients. Discovery supplies which relay is alive and its port; the
// externally reachable host comes from publicHost (DB setting
// beam.public_host) — the relay's own registered IP is an internal
// overlay address a desktop client can't reach. Manual override (DB
// setting beam.relay_address) still wins outright for incident routing.
func resolveRelay(ctx context.Context, rdb *redis.Client, manualOverride, publicHost string) (string, string) {
	if strings.TrimSpace(manualOverride) != "" {
		return manualOverride, "manual"
	}
	if info, ok := PickBeamRelay(ctx, rdb); ok {
		// The configured public host is the single source of truth for
		// the externally reachable hostname — it overrides whatever the
		// relay reported about itself.
		if h := strings.TrimSpace(publicHost); h != "" {
			info.PublicHost = h
		}
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

	// server_uuid is read from the query string (GET) or a JSON body
	// (POST). The Beam desktop app uses GET — networks in front of Core
	// were observed handing its POSTs an HTML page instead of routing
	// them through, and GET goes straight to the handler.
	serverUUID := r.URL.Query().Get("server_uuid")
	if serverUUID == "" && r.Body != nil {
		var req struct {
			ServerUUID string `json:"server_uuid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		serverUUID = req.ServerUUID
	}
	if serverUUID == "" {
		sendJSONError(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	// Resolve server
	server, err := h.state.Store.GetServerByUUID(serverUUID)
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

	// Resolve node discovery ID (Token field = DYLARIS_NODE_ID) + direct-connect
	// hint inputs. Whether these are actually handed out is decided below by relay
	// presence, not by node ownership.
	nodeDiscoveryID := ""
	var nodePrivateIPs []string
	var nodePublicIP string
	if server.NodeID > 0 {
		node, err := h.state.Store.GetNodeByID(server.NodeID)
		if err == nil && node != nil {
			nodeDiscoveryID = node.Token
			nodePrivateIPs = node.PrivateIPs
			nodePublicIP = node.PublicIP
		}
	}
	if nodeDiscoveryID == "" {
		sendJSONError(w, "Server has no assigned node", http.StatusBadRequest)
		return
	}

	// Fingerprint of the node's deterministic LAN TLS cert so the app can pin it
	// (encryption + MITM protection on the direct hop, LAN or public). Derived from
	// the same beam secret + node ID the node uses to serve that listener. Needed
	// for any direct target, so derive it up front; an error leaves it empty and
	// buildBeamDirectHints then advertises no direct path.
	directFingerprint := ""
	if fp, ferr := beamauth.LANCertFingerprint(h.jwtSecret, nodeDiscoveryID); ferr == nil {
		directFingerprint = fp
	}

	// Presence-driven gate: when a relay is registered we obscure the node and omit
	// direct hints; when none is, we hand the client the node's direct addresses
	// (LAN IPs + public IP on the pinned-TLS beam port) so a solo deployment can
	// connect without a gateway. resolveRelay reads the same settings GetBeamConfig
	// uses.
	getSetting := func(key string) string {
		val, _ := h.state.Store.GetSetting(key)
		return val
	}
	relayAddr, _ := resolveRelay(r.Context(), h.state.Redis, getSetting("beam.relay_address"), getSetting("beam.public_host"))
	directHints := buildBeamDirectHints(relayAddr, nodePrivateIPs, nodePublicIP, beamLANPort, directFingerprint)

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

	// expires is informational. Compute it from the TTL directly - SignBeamTicket
	// takes claims by value and sets ExpiresAt on its own copy, so claims.ExpiresAt
	// here is still nil; dereferencing it panicked the handler (the connection drop
	// surfaced as a 502).
	// Direct-connect hints (lanHints): the node's LAN IPs + public address on the
	// pinned-TLS beam port, handed out ONLY when no relay is present so a solo
	// client can reach the node directly. Omitted when a relay is registered - the
	// client uses the relay and the node IP stays hidden. The JWT ticket gates auth
	// (node+server bound) on either path. Returned only to a caller already
	// authorized for this server.
	resp := map[string]interface{}{
		"success": true,
		"ticket":  ticketString,
		"expires": time.Now().Add(beamauth.BeamTicketTTL).Unix(),
	}
	if directHints != nil {
		resp["lanHints"] = directHints
	}
	json.NewEncoder(w).Encode(resp)
}

// beamLANPort is the node's LAN fast-path TLS port (BEAM_LAN_PORT default). The
// app probes the node's private IPs on this port, dials TLS, and pins the
// fingerprint above. Distinct from the plain overlay port (25521) used by the relay.
const beamLANPort = "25523"

// beamDirectHints is the presence-driven direct-connect payload on a beam ticket
// (serialized as the JSON field "lanHints"). It carries the node's LAN IPs and
// public address on the pinned-TLS beam port so a solo client (no relay present)
// can dial the node directly. Absent from the ticket when a relay is registered.
type beamDirectHints struct {
	IPs         []string `json:"ips"`
	PublicAddr  string   `json:"publicAddr"`
	Port        string   `json:"port"`
	Fingerprint string   `json:"fingerprint"`
}

// buildBeamDirectHints applies the presence-driven gate. When a relay is
// registered (relayAddr != "") it returns nil: the client must use the relay so
// the node IP stays hidden. Otherwise it returns the node's dialable direct
// targets (LAN IPs + public address) on the pinned-TLS beam port. Returns nil when
// there is no fingerprint to pin (never advertise an unpinnable, plaintext-risk
// path) or when nothing is dialable. Pure (no I/O) so it is unit-tested directly.
func buildBeamDirectHints(relayAddr string, lanIPs []string, publicIP, port, fingerprint string) *beamDirectHints {
	if strings.TrimSpace(relayAddr) != "" {
		return nil // relay present -> obscure the node, hand out no direct hints
	}
	if fingerprint == "" {
		return nil // no pin -> refuse to advertise an unpinnable direct path
	}
	h := &beamDirectHints{IPs: lanIPs, Port: port, Fingerprint: fingerprint}
	if ip := strings.TrimSpace(publicIP); ip != "" {
		h.PublicAddr = net.JoinHostPort(ip, port)
	}
	if len(h.IPs) == 0 && h.PublicAddr == "" {
		return nil // nothing to dial
	}
	return h
}

// GetBeamConfig returns the Beam relay address and branding info.
// GET /api/beam/config
func (h *BeamHandler) GetBeamConfig(w http.ResponseWriter, r *http.Request) {
	getSetting := func(key string) string {
		val, _ := h.state.Store.GetSetting(key)
		return val
	}

	manualOverride := getSetting("beam.relay_address")
	publicHost := getSetting("beam.public_host")
	relayAddress, _ := resolveRelay(r.Context(), h.state.Redis, manualOverride, publicHost)
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
//
// Tight connect + response-header timeouts so a missing/unreachable
// relay surfaces as a 502 in a few seconds instead of leaving the
// browser spinning for minutes.
var downloadRelayClient = &http.Client{
	Timeout: 30 * time.Minute, // total — leaves room for slow networks on large binaries
	Transport: &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second, // connect timeout
		}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
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

	// Try every discovered candidate in turn. The first relay's overlay
	// IP can be unreachable from Core if Core happens to live in a
	// different overlay (multi-stack deploys), so we fall through to
	// the public host name and then the next relay in sequence.
	candidates := resolveDownloadCandidates(r.Context(), h.state.Redis, getSetting, platform)
	if len(candidates) == 0 {
		sendJSONError(w, "No Beam relay available", http.StatusServiceUnavailable)
		return
	}

	var resp *http.Response
	var lastErr error
	var winningURL string
	for _, upstream := range candidates {
		log.Printf("beam-download: trying upstream %s for platform=%s", upstream, platform)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
		if err != nil {
			lastErr = fmt.Errorf("build request: %w", err)
			continue
		}
		if ua := r.Header.Get("User-Agent"); ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		r2, doErr := downloadRelayClient.Do(req)
		if doErr != nil {
			log.Printf("beam-download: %s failed: %v", upstream, doErr)
			lastErr = doErr
			continue
		}
		if r2.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(r2.Body, 512))
			r2.Body.Close()
			log.Printf("beam-download: %s returned %d: %s", upstream, r2.StatusCode, strings.TrimSpace(string(body)))
			lastErr = fmt.Errorf("relay returned %d", r2.StatusCode)
			continue
		}
		resp = r2
		winningURL = upstream
		break
	}
	if resp == nil {
		msg := "Could not reach any Beam relay"
		if lastErr != nil {
			msg += ": " + lastErr.Error()
		}
		sendJSONError(w, msg, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	log.Printf("beam-download: streaming %s (%s) for platform=%s", winningURL, resp.Header.Get("Content-Length"), platform)

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

// resolveDownloadCandidates returns an ordered list of upstream URLs the
// download proxy should try, best-first.
//
// This is a Core→Relay server-to-server fetch. It uses the relays
// registered in Redis (beam:registry:*) directly — strictly internal
// coordinates (overlay IP, private IP, BEAM_ID via Docker-DNS). The
// client-facing beam.relay_address / beam.public_host settings are
// deliberately NOT considered here: they pin where Beam.exe connects
// from outside, where the download port (25552) is typically closed.
//
// The Beam app is published to Cloudflare R2 (no longer served by the relay).
// Resolution order:
//   1. beam.download_link — explicit full-URL override (CDN/mirror).
//   2. The platform URL from the R2 manifest (beam.release_manifest, default
//      https://downloads.dylaris.com/beam/latest.json). Public, so Core fetches
//      it without auth.
func resolveDownloadCandidates(ctx context.Context, rdb *redis.Client, getSetting func(string) string, platform string) []string {
	if link := strings.TrimSpace(getSetting("beam.download_link")); link != "" {
		base := strings.TrimRight(link, "/")
		if strings.Contains(base, "/download/") {
			return []string{base}
		}
		return []string{base + "/download/" + platform}
	}

	manifestURL := strings.TrimSpace(getSetting("beam.release_manifest"))
	if manifestURL == "" {
		manifestURL = "https://downloads.dylaris.com/beam/latest.json"
	}
	if u, err := manifestPlatformURL(ctx, manifestURL, platform); err == nil && u != "" {
		return []string{u}
	}
	return nil
}

// manifestPlatformURL fetches the R2 release manifest and returns the download
// URL for the given platform slug (e.g. windows-amd64).
func manifestPlatformURL(ctx context.Context, manifestURL, platform string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("manifest: %d", resp.StatusCode)
	}
	var m struct {
		Platforms map[string]struct {
			URL string `json:"url"`
		} `json:"platforms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return "", err
	}
	if p, ok := m.Platforms[platform]; ok && p.URL != "" {
		return p.URL, nil
	}
	return "", fmt.Errorf("no manifest entry for %s", platform)
}
