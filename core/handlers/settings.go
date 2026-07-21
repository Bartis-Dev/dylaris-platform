package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type SettingsHandler struct {
	state *AppState
}

func NewSettingsHandler(state *AppState) *SettingsHandler {
	return &SettingsHandler{state: state}
}

// NOTE: the legacy /settings/library CRUD (GetLibrarySettings/SaveLibrarySettings)
// and its /settings/library/test probe (TestLibraryConnection, further down this
// file) were removed. Since the Core-stateless-storage rework, Library reads its
// provider exclusively from the shared Core file storage config
// (handlers.LibraryHandler.buildProvider -> AppState.buildCoreStorageProvider);
// the library_type/library_path/library_s3_* settings these handlers read/wrote
// were dead - SaveLibrarySettings' "success" response changed nothing observable,
// and TestLibraryConnection's "ok" was unrelated to the real (Core storage)
// provider. That live UI trap is gone; configure storage at Settings -> Core
// Storage (CoreStorageHandler, /api/settings/core-storage*) instead.

// --- File Manager Settings ---

type FileManagerSettings struct {
	AdminUploadLimit   int64 `json:"adminUploadLimit"`   // bytes
	AdminDownloadLimit int64 `json:"adminDownloadLimit"` // bytes
	UserUploadLimit    int64 `json:"userUploadLimit"`    // bytes
	UserDownloadLimit  int64 `json:"userDownloadLimit"`  // bytes
}

var defaultFileManagerSettings = FileManagerSettings{
	AdminUploadLimit:   2 * 1024 * 1024 * 1024, // 2 GB
	AdminDownloadLimit: 5 * 1024 * 1024 * 1024, // 5 GB
	UserUploadLimit:    500 * 1024 * 1024,      // 500 MB
	UserDownloadLimit:  1 * 1024 * 1024 * 1024, // 1 GB
}

// GetFileManagerSettings GET /api/settings/filemanager - PANEL settings.read (RequireCap at the route).
func (h *SettingsHandler) GetFileManagerSettings(w http.ResponseWriter, r *http.Request) {
	settings := h.loadFileManagerSettings()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": settings,
	})
}

// SaveFileManagerSettings POST /api/settings/filemanager - PANEL settings.write (RequireCap at the route).
func (h *SettingsHandler) SaveFileManagerSettings(w http.ResponseWriter, r *http.Request) {
	var req FileManagerSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	pairs := []struct{ k, v string }{
		{"fm.admin_upload_limit", fmt.Sprintf("%d", req.AdminUploadLimit)},
		{"fm.admin_download_limit", fmt.Sprintf("%d", req.AdminDownloadLimit)},
		{"fm.user_upload_limit", fmt.Sprintf("%d", req.UserUploadLimit)},
		{"fm.user_download_limit", fmt.Sprintf("%d", req.UserDownloadLimit)},
	}

	for _, p := range pairs {
		if err := h.state.Store.SetSetting(p.k, p.v); err != nil {
			sendJSONError(w, "Failed to save setting: "+p.k, http.StatusInternalServerError)
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *SettingsHandler) loadFileManagerSettings() FileManagerSettings {
	settings := defaultFileManagerSettings

	getInt64 := func(key string, def int64) int64 {
		val, err := h.state.Store.GetSetting(key)
		if err != nil || val == "" {
			return def
		}
		var n int64
		if _, err := fmt.Sscanf(val, "%d", &n); err == nil && n > 0 {
			return n
		}
		return def
	}

	settings.AdminUploadLimit = getInt64("fm.admin_upload_limit", settings.AdminUploadLimit)
	settings.AdminDownloadLimit = getInt64("fm.admin_download_limit", settings.AdminDownloadLimit)
	settings.UserUploadLimit = getInt64("fm.user_upload_limit", settings.UserUploadLimit)
	settings.UserDownloadLimit = getInt64("fm.user_download_limit", settings.UserDownloadLimit)

	return settings
}

// GetUploadLimitForUser returns the upload limit in bytes for the current user
func (h *SettingsHandler) GetUploadLimitForUser(isAdmin bool) int64 {
	settings := h.loadFileManagerSettings()
	if isAdmin {
		return settings.AdminUploadLimit
	}
	return settings.UserUploadLimit
}

// GetDownloadLimitForUser returns the download limit in bytes for the current user
func (h *SettingsHandler) GetDownloadLimitForUser(isAdmin bool) int64 {
	settings := h.loadFileManagerSettings()
	if isAdmin {
		return settings.AdminDownloadLimit
	}
	return settings.UserDownloadLimit
}

// GetUserLimits GET /api/settings/filemanager/limits — available to ALL authenticated users
func (h *SettingsHandler) GetUserLimits(w http.ResponseWriter, r *http.Request) {
	isAdmin := IsAdmin(r)
	settings := h.loadFileManagerSettings()

	var uploadLimit, downloadLimit int64
	if isAdmin {
		uploadLimit = settings.AdminUploadLimit
		downloadLimit = settings.AdminDownloadLimit
	} else {
		uploadLimit = settings.UserUploadLimit
		downloadLimit = settings.UserDownloadLimit
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"uploadLimit":   uploadLimit,
		"downloadLimit": downloadLimit,
	})
}

// --- Feature Settings ---

type FeatureSettings struct {
	ProxyEnabled   bool `json:"proxyEnabled"`
	GatewayEnabled bool `json:"gatewayEnabled"`
}

// GetFeatureSettings GET /api/settings/features
func (h *SettingsHandler) GetFeatureSettings(w http.ResponseWriter, r *http.Request) {
	settings := h.LoadFeatureSettings()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": settings,
	})
}

// SaveFeatureSettings POST /api/settings/features - PANEL settings.write (RequireCap at the route).
func (h *SettingsHandler) SaveFeatureSettings(w http.ResponseWriter, r *http.Request) {
	var req FeatureSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	proxyVal := "false"
	if req.ProxyEnabled {
		proxyVal = "true"
	}
	gatewayVal := "false"
	if req.GatewayEnabled {
		gatewayVal = "true"
	}

	if err := h.state.Store.SetSetting("feature_proxy_enabled", proxyVal); err != nil {
		sendJSONError(w, "Failed to save setting", http.StatusInternalServerError)
		return
	}
	if err := h.state.Store.SetSetting("feature_gateway_enabled", gatewayVal); err != nil {
		sendJSONError(w, "Failed to save setting", http.StatusInternalServerError)
		return
	}

	h.state.Events.Publish(r.Context(), "features.changed", nil)

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// LoadFeatureSettings reads feature flags from the database. Available to all authenticated users.
func (h *SettingsHandler) LoadFeatureSettings() FeatureSettings {
	proxyVal, _ := h.state.Store.GetSetting("feature_proxy_enabled")
	gatewayVal, _ := h.state.Store.GetSetting("feature_gateway_enabled")
	return FeatureSettings{
		ProxyEnabled:   proxyVal != "false",   // default true
		GatewayEnabled: gatewayVal != "false", // default true
	}
}

// --- Gateway Settings ---

type GatewaySettings struct {
	Limits               GatewayLimits  `json:"limits"`
	HosterDomains        []HosterDomain `json:"hosterDomains"`
	CustomDomainsEnabled bool           `json:"customDomainsEnabled"`
	CnameTarget          string         `json:"cnameTarget"`
	// BlockedRoutePrefixes are leftmost labels users may not register as a route
	// (e.g. "admin", "dylaris"). Applies to the hoster-subdomain picker and the
	// leftmost label of custom/raw domains. Even though only :25565 MC traffic is
	// routed, reserving these keeps confusable / impersonating names off the table.
	BlockedRoutePrefixes []string `json:"blockedRoutePrefixes"`
}

// defaultBlockedRoutePrefixes seeds a protective reserved list when the admin has
// never saved one. Once saved (even empty), the admin's list wins.
var defaultBlockedRoutePrefixes = []string{
	"admin", "dylaris", "app", "api", "www", "panel", "gateway", "edge", "hub",
	"link", "warp", "beam", "mail", "ns", "status", "support", "staff", "system", "root",
}

type GatewayLimits struct {
	Global        int  `json:"global"`
	UserDefault   int  `json:"userDefault"`
	PerServer     int  `json:"perServer"`
	PortMc        int  `json:"portMc"`
	PortMcEnabled bool `json:"portMcEnabled"`
}

// HosterDomain is one of the platform-provided base domains under which a
// user can register a route by entering only a subdomain. The validation
// mode controls what characters are accepted in that subdomain field.
type HosterDomain struct {
	Domain     string `json:"domain"`     // e.g. "dylaris.com"
	Validation string `json:"validation"` // "letters" | "alphanumeric" | "dns"
}

// validHosterValidation returns true for the three accepted modes —
// kept narrow on purpose so future modes have to be added explicitly.
func validHosterValidation(v string) bool {
	return v == "letters" || v == "alphanumeric" || v == "dns"
}

// GetGatewaySettings GET /api/settings/gateway - PANEL settings.read (RequireCap at the route).
func (h *SettingsHandler) GetGatewaySettings(w http.ResponseWriter, r *http.Request) {
	getSetting := func(key string) string {
		val, _ := h.state.Store.GetSetting(key)
		return val
	}

	getLimit := func(scope string) int {
		l, err := h.state.Store.GetGatewayRouteLimit(scope)
		if err != nil {
			return 0
		}
		return l.MaxRoutes
	}

	settings := GatewaySettings{
		Limits: GatewayLimits{
			Global:        getLimit("global"),
			UserDefault:   getLimit("user_default"),
			PerServer:     getLimit("per_server"),
			PortMc:        getLimit("port:25565"),
			PortMcEnabled: getSetting("gateway_port_mc_enabled") != "false",
		},
		HosterDomains:        h.loadHosterDomains(),
		CustomDomainsEnabled: getSetting("gateway_custom_domains_enabled") == "true",
		CnameTarget:          getSetting("gateway_cname_target"),
		BlockedRoutePrefixes: h.loadBlockedRoutePrefixes(),
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": settings,
	})
}

// loadHosterDomains parses the persisted hoster-domain list from settings.
// Returns an empty slice if unset or malformed.
func (h *SettingsHandler) loadHosterDomains() []HosterDomain {
	raw, _ := h.state.Store.GetSetting("gateway_hoster_domains")
	if raw == "" {
		return []HosterDomain{}
	}
	var out []HosterDomain
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []HosterDomain{}
	}
	return out
}

// loadBlockedRoutePrefixes parses the persisted reserved-prefix list. When the
// setting was never saved (raw == "") it falls back to the protective default;
// once the admin saves a list (even an empty one) that explicit choice is used.
func (h *SettingsHandler) loadBlockedRoutePrefixes() []string {
	raw, _ := h.state.Store.GetSetting("gateway_blocked_route_prefixes")
	if raw == "" {
		return append([]string(nil), defaultBlockedRoutePrefixes...)
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	return out
}

// LoadGatewayDomainConfig is the read-side helper for code paths that need
// just the domain configuration (route create handler, public route-options
// endpoint) without re-running the admin-only GetGatewaySettings.
func (h *SettingsHandler) LoadGatewayDomainConfig() (hosters []HosterDomain, customEnabled bool, cnameTarget string) {
	hosters = h.loadHosterDomains()
	v, _ := h.state.Store.GetSetting("gateway_custom_domains_enabled")
	customEnabled = v == "true"
	cnameTarget, _ = h.state.Store.GetSetting("gateway_cname_target")
	return
}

// GetGatewayRouteOptions GET /api/gateway/route-options
// Available to all authenticated users — the user-facing route form needs
// the hoster list + custom-domain config to render itself.
func (h *SettingsHandler) GetGatewayRouteOptions(w http.ResponseWriter, r *http.Request) {
	hosters, customEnabled, cname := h.LoadGatewayDomainConfig()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":              true,
		"hosterDomains":        hosters,
		"customDomainsEnabled": customEnabled,
		"cnameTarget":          cname,
	})
}

// SaveGatewaySettings POST /api/settings/gateway - PANEL settings.write (RequireCap at the route).
func (h *SettingsHandler) SaveGatewaySettings(w http.ResponseWriter, r *http.Request) {
	var req GatewaySettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate + normalize hoster domains
	cleaned := make([]HosterDomain, 0, len(req.HosterDomains))
	seen := map[string]bool{}
	for _, hd := range req.HosterDomains {
		dom := strings.ToLower(strings.TrimSpace(hd.Domain))
		if dom == "" {
			continue
		}
		if !domainRegex.MatchString(dom) {
			sendJSONError(w, "Invalid hoster domain: "+dom, http.StatusBadRequest)
			return
		}
		if seen[dom] {
			sendJSONError(w, "Duplicate hoster domain: "+dom, http.StatusBadRequest)
			return
		}
		seen[dom] = true
		val := strings.ToLower(strings.TrimSpace(hd.Validation))
		if !validHosterValidation(val) {
			val = "alphanumeric"
		}
		cleaned = append(cleaned, HosterDomain{Domain: dom, Validation: val})
	}
	hostersJSON, _ := json.Marshal(cleaned)

	// Normalize the reserved-prefix list: lowercase, trimmed, deduped, non-empty.
	blockedSeen := map[string]bool{}
	blocked := make([]string, 0, len(req.BlockedRoutePrefixes))
	for _, p := range req.BlockedRoutePrefixes {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || blockedSeen[p] {
			continue
		}
		blockedSeen[p] = true
		blocked = append(blocked, p)
	}
	blockedJSON, _ := json.Marshal(blocked)

	// Save port-enable settings
	portSettings := []struct{ k, v string }{
		{"gateway_port_mc_enabled", fmt.Sprintf("%t", req.Limits.PortMcEnabled)},
		{"gateway_hoster_domains", string(hostersJSON)},
		{"gateway_custom_domains_enabled", fmt.Sprintf("%t", req.CustomDomainsEnabled)},
		{"gateway_cname_target", strings.TrimSpace(req.CnameTarget)},
		{"gateway_blocked_route_prefixes", string(blockedJSON)},
	}
	for _, p := range portSettings {
		if err := h.state.Store.SetSetting(p.k, p.v); err != nil {
			sendJSONError(w, "Failed to save setting: "+p.k, http.StatusInternalServerError)
			return
		}
	}

	// Save limits
	limits := []struct {
		scope string
		max   int
	}{
		{"global", req.Limits.Global},
		{"user_default", req.Limits.UserDefault},
		{"per_server", req.Limits.PerServer},
		{"port:25565", req.Limits.PortMc},
	}
	for _, l := range limits {
		if err := h.state.Store.SetGatewayRouteLimit(l.scope, l.max); err != nil {
			sendJSONError(w, "Failed to save limit: "+l.scope, http.StatusInternalServerError)
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// --- Placement Settings ---

// PlacementSettings holds the global defaults used when a new node is
// registered. Per-node overrides are stored directly on the nodes row.
type PlacementSettings struct {
	CPUOvercommitDefault float64 `json:"cpuOvercommitDefault"`
	RAMOvercommitDefault float64 `json:"ramOvercommitDefault"`
	DiskBufferGB         int     `json:"diskBufferGb"`
	RebalanceEnabled     bool    `json:"rebalanceEnabled"`
	RebalanceThreshold   int     `json:"rebalanceThreshold"` // % at which a node is considered overloaded
	PortMode             string  `json:"portMode"`           // "sequential" | "random" — host-port allocation strategy
	ContainerPort        int     `json:"containerPort"`      // default MC port inside the container (usually 25565)
	// PidsLimit caps the process/thread count per server container (cgroup pids
	// controller) as an anti fork-bomb guard. 0 = unlimited (default). Counts
	// threads too, so set it generously (e.g. 4096) — too low throttles heavy
	// modded servers.
	PidsLimit int64 `json:"pidsLimit"`
	// IOWeight is the cgroup blkio relative weight (10–1000) applied to every
	// server container, so a noisy neighbour can't starve others of disk I/O.
	// 0 = unset (default). NOTE: this is a RELATIVE priority, not a hard cap, and
	// only takes effect with an I/O scheduler that honours blkio weight (BFQ/CFQ);
	// on blk-mq with none/mq-deadline it is a no-op. Hard per-device bps caps need
	// the backing block device and are intentionally not wired here.
	IOWeight uint16 `json:"ioWeight"`
}

var defaultPlacementSettings = PlacementSettings{
	CPUOvercommitDefault: 2.0, // CPU is time-shared, 2.0x = 200% is conservative
	RAMOvercommitDefault: 1.0, // RAM has no default overcommit (safer); 100%
	DiskBufferGB:         10,
	RebalanceEnabled:     false,
	RebalanceThreshold:   90,
	PortMode:             "sequential",
	ContainerPort:        25565,
	PidsLimit:            0, // unlimited by default — opt-in anti fork-bomb cap
	IOWeight:             0, // unset by default — opt-in blkio fair-share
}

// GetPlacementSettings GET /api/settings/placement - PANEL settings.read (RequireCap at the route).
func (h *SettingsHandler) GetPlacementSettings(w http.ResponseWriter, r *http.Request) {
	s := h.LoadPlacementSettings()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": s,
	})
}

// SavePlacementSettings POST /api/settings/placement - PANEL settings.write (RequireCap at the route).
func (h *SettingsHandler) SavePlacementSettings(w http.ResponseWriter, r *http.Request) {
	var req PlacementSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.CPUOvercommitDefault <= 0 || req.RAMOvercommitDefault <= 0 {
		sendJSONError(w, "Overcommit ratios must be > 0", http.StatusBadRequest)
		return
	}
	if req.DiskBufferGB < 0 {
		req.DiskBufferGB = 0
	}
	if req.RebalanceThreshold < 50 {
		req.RebalanceThreshold = 50
	}
	if req.RebalanceThreshold > 100 {
		req.RebalanceThreshold = 100
	}
	if req.PortMode != "sequential" && req.PortMode != "random" {
		req.PortMode = "sequential"
	}
	if req.ContainerPort <= 0 || req.ContainerPort > 65535 {
		req.ContainerPort = 25565
	}
	if req.PidsLimit < 0 {
		req.PidsLimit = 0
	}
	// blkio weight is valid only in 10–1000; clamp non-zero values into range.
	if req.IOWeight != 0 {
		if req.IOWeight < 10 {
			req.IOWeight = 10
		} else if req.IOWeight > 1000 {
			req.IOWeight = 1000
		}
	}

	pairs := []struct{ k, v string }{
		{"placement.cpu_overcommit_default", fmt.Sprintf("%g", req.CPUOvercommitDefault)},
		{"placement.ram_overcommit_default", fmt.Sprintf("%g", req.RAMOvercommitDefault)},
		{"placement.disk_buffer_gb", fmt.Sprintf("%d", req.DiskBufferGB)},
		{"placement.rebalance_enabled", fmt.Sprintf("%t", req.RebalanceEnabled)},
		{"placement.rebalance_threshold", fmt.Sprintf("%d", req.RebalanceThreshold)},
		{"placement.port_mode", req.PortMode},
		{"placement.container_port", fmt.Sprintf("%d", req.ContainerPort)},
		{"placement.pids_limit", fmt.Sprintf("%d", req.PidsLimit)},
		{"placement.io_weight", fmt.Sprintf("%d", req.IOWeight)},
	}
	for _, p := range pairs {
		if err := h.state.Store.SetSetting(p.k, p.v); err != nil {
			sendJSONError(w, "Failed to save setting: "+p.k, http.StatusInternalServerError)
			return
		}
	}

	// Publish to Redis so nodes pick up the new values via loadModesFromRedis
	// without needing a redeploy. Best-effort — nodes also re-read every 30s.
	if h.state.Redis != nil {
		ctx := r.Context()
		h.state.Redis.Set(ctx, "dylaris:placement:port_mode", req.PortMode, 0)
		h.state.Redis.Set(ctx, "dylaris:placement:container_port", fmt.Sprintf("%d", req.ContainerPort), 0)
		h.state.Redis.Set(ctx, "dylaris:placement:pids_limit", fmt.Sprintf("%d", req.PidsLimit), 0)
		h.state.Redis.Set(ctx, "dylaris:placement:io_weight", fmt.Sprintf("%d", req.IOWeight), 0)
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// LoadPlacementSettings reads placement settings with sensible defaults.
func (h *SettingsHandler) LoadPlacementSettings() PlacementSettings {
	s := defaultPlacementSettings

	getStr := func(k string) string { v, _ := h.state.Store.GetSetting(k); return v }

	if v := getStr("placement.cpu_overcommit_default"); v != "" {
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err == nil && f > 0 {
			s.CPUOvercommitDefault = f
		}
	}
	if v := getStr("placement.ram_overcommit_default"); v != "" {
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err == nil && f > 0 {
			s.RAMOvercommitDefault = f
		}
	}
	if v := getStr("placement.disk_buffer_gb"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 {
			s.DiskBufferGB = n
		}
	}
	if v := getStr("placement.rebalance_enabled"); v != "" {
		s.RebalanceEnabled = v == "true"
	}
	if v := getStr("placement.rebalance_threshold"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 50 && n <= 100 {
			s.RebalanceThreshold = n
		}
	}
	if v := getStr("placement.port_mode"); v == "sequential" || v == "random" {
		s.PortMode = v
	}
	if v := getStr("placement.container_port"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 && n <= 65535 {
			s.ContainerPort = n
		}
	}
	if v := getStr("placement.pids_limit"); v != "" {
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 {
			s.PidsLimit = n
		}
	}
	if v := getStr("placement.io_weight"); v != "" {
		var n uint16
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && (n == 0 || (n >= 10 && n <= 1000)) {
			s.IOWeight = n
		}
	}
	return s
}

// --- Server Settings ---

type ServerSettings struct {
	MaxSubServers int `json:"maxSubServers"` // 0 = unlimited
}

var defaultServerSettings = ServerSettings{
	MaxSubServers: 3,
}

// GetServerSettings GET /api/settings/servers - PANEL settings.read (RequireCap at the route).
func (h *SettingsHandler) GetServerSettings(w http.ResponseWriter, r *http.Request) {
	settings := h.LoadServerSettings()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": settings,
	})
}

// SaveServerSettings POST /api/settings/servers - PANEL settings.write (RequireCap at the route).
func (h *SettingsHandler) SaveServerSettings(w http.ResponseWriter, r *http.Request) {
	var req ServerSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.state.Store.SetSetting("srv.max_sub_servers", fmt.Sprintf("%d", req.MaxSubServers)); err != nil {
		sendJSONError(w, "Failed to save setting", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// LoadServerSettings reads server settings from the database.
func (h *SettingsHandler) LoadServerSettings() ServerSettings {
	settings := defaultServerSettings

	if val, err := h.state.Store.GetSetting("srv.max_sub_servers"); err == nil && val != "" {
		var n int
		if _, err := fmt.Sscanf(val, "%d", &n); err == nil && n >= 0 {
			settings.MaxSubServers = n
		}
	}

	return settings
}

// ─── Beam Settings ───────────────────────────────────────────────────

type BeamSettings struct {
	RelayAddress     string          `json:"relayAddress"`     // Effective relay (discovered or manual override)
	ManualOverride   string          `json:"manualOverride"`   // Admin-configured override (empty = use auto-discovery)
	PublicHost       string          `json:"publicHost"`       // Externally reachable hostname for discovered relays (e.g. beam.dylaris.com)
	DiscoveredRelays []BeamRelayInfo `json:"discoveredRelays"` // Currently registered relays (read-only)
	// BwLimit is the legacy single-value Node throttle (bytes/sec, 0 =
	// unlimited). It stays the source of truth for the actual Node-side
	// rate.Limiter so older deploys keep working. The four-direction
	// fields below are saved alongside and will replace it once asymmetric
	// node + dedicated relay throttles ship.
	BwLimit      int64  `json:"bwLimit"`
	Enabled      bool   `json:"enabled"`
	DownloadLink string `json:"downloadLink"` // Optional CDN URL — overrides relay-served download

	// MinVersion is the Beam force-update floor (empty = gating off). Persisted
	// to DB key beam.min_version; advertised by GetBeamConfig and enforced by
	// GetBeamTicket. Validated empty-or-semver on save.
	MinVersion string `json:"minVersion"`

	// MinVersionMode selects how the force-update floor is chosen: "manual"
	// (default) uses MinVersion above; "auto" follows the minVersion baked into
	// the SIGNED release manifest (verified by Core, see effectiveMinVersion).
	// Persisted to DB key beam.min_version_mode.
	MinVersionMode string `json:"minVersionMode"`

	// DevChannelAccess gates who may opt into the dev (prerelease) update
	// channel: "disabled" (default), "admins-only", or "all-users". Persisted to
	// DB key beam.dev_channel_access; enforced by SetMyBeamChannel + GetBeamConfig.
	DevChannelAccess string `json:"devChannelAccess"`

	// Throttle splits (bytes/sec, 0 = unlimited). Stored verbatim. Until
	// the per-direction limiters land in node + relay these are advisory:
	// BwLimit (above) is computed by SaveBeamSettings as the lower of
	// the two internal directions so existing behavior is preserved.
	BwUpInternal   int64 `json:"bwUpInternal"`
	BwDownInternal int64 `json:"bwDownInternal"`
	BwUpExternal   int64 `json:"bwUpExternal"`
	BwDownExternal int64 `json:"bwDownExternal"`

	// Reference values the admin records about the host hardware
	// (datacenter internal + external uplink). Pure informational —
	// never enforced. Help operators size their throttle values.
	RefUpInternal   int64 `json:"refUpInternal"`
	RefDownInternal int64 `json:"refDownInternal"`
	RefUpExternal   int64 `json:"refUpExternal"`
	RefDownExternal int64 `json:"refDownExternal"`

	// Upload limits (bytes, 0 = unlimited). Enforced by the node on the beam
	// upload path, which bypasses Core's HTTP body-size cap and disk precheck.
	// MaxUploadBytes is an absolute per-upload cap; DailyUploadBytes is a
	// per-user daily total. Published to Redis keys beam:max_upload_bytes /
	// beam:daily_upload_bytes, which the node reads per upload.
	MaxUploadBytes   int64 `json:"maxUploadBytes"`
	DailyUploadBytes int64 `json:"dailyUploadBytes"`
}

// GetBeamSettings GET /api/settings/beam — all authenticated users (relay address + download link needed in Files tab)
func (h *SettingsHandler) GetBeamSettings(w http.ResponseWriter, r *http.Request) {
	settings := h.LoadBeamSettings()
	settings.DiscoveredRelays = DiscoverBeamRelays(r.Context(), h.state.Redis)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": settings,
	})
}

// SaveBeamSettings POST /api/settings/beam - PANEL settings.write (RequireCap at the route).
func (h *SettingsHandler) SaveBeamSettings(w http.ResponseWriter, r *http.Request) {
	var req BeamSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// The force-update floor must be empty (gating off) or a valid semver;
	// reject a malformed value so an admin cannot set a floor that never
	// matches (and never silently locks every client out).
	minVersion := strings.TrimSpace(req.MinVersion)
	if minVersion != "" {
		if _, ok := parseBeamSemver(minVersion); !ok {
			sendJSONError(w, "Invalid minimum version: must be empty or a semantic version like 1.2.3", http.StatusBadRequest)
			return
		}
	}

	// Min-version mode: empty normalizes to "manual" (the default); only
	// "manual" or "auto" are accepted so an unknown mode can never silently
	// change how the floor is resolved.
	minVersionMode := strings.TrimSpace(req.MinVersionMode)
	if minVersionMode == "" {
		minVersionMode = beamMinVersionModeManual
	}
	if minVersionMode != beamMinVersionModeManual && minVersionMode != beamMinVersionModeAuto {
		sendJSONError(w, "Invalid min-version mode: must be 'manual' or 'auto'", http.StatusBadRequest)
		return
	}

	// Dev-channel access policy: empty normalizes to "disabled" (default);
	// only the three known values are accepted so an unknown policy can never
	// silently widen who reaches the prerelease channel.
	devChannelAccess := strings.TrimSpace(req.DevChannelAccess)
	if devChannelAccess == "" {
		devChannelAccess = beamDevAccessDisabled
	}
	if devChannelAccess != beamDevAccessDisabled && devChannelAccess != beamDevAccessAdminsOnly && devChannelAccess != beamDevAccessAllUsers {
		sendJSONError(w, "Invalid dev-channel access: must be 'disabled', 'admins-only', or 'all-users'", http.StatusBadRequest)
		return
	}

	enabledStr := "false"
	if req.Enabled {
		enabledStr = "true"
	}

	// Until per-direction throttles exist, the effective Node cap is the
	// lower bound of the two internal directions (file write/read both
	// flow over the internal hop). Operators see the same effect they'd
	// get from the legacy single field if they leave the splits at 0.
	effectiveBw := req.BwLimit
	if req.BwUpInternal > 0 || req.BwDownInternal > 0 {
		effectiveBw = minNonZeroInt64(req.BwUpInternal, req.BwDownInternal)
	}

	pairs := []struct{ k, v string }{
		{"beam.relay_address", req.RelayAddress},
		{"beam.public_host", strings.TrimSpace(req.PublicHost)},
		{"beam.bw_limit", fmt.Sprintf("%d", effectiveBw)},
		{"beam.enabled", enabledStr},
		{"beam.download_link", req.DownloadLink},
		{"beam.min_version", minVersion},
		{"beam.min_version_mode", minVersionMode},
		{"beam.dev_channel_access", devChannelAccess},

		// New per-direction throttle splits (advisory until relay-side
		// throttle ships). Stored verbatim so the UI round-trips.
		{"beam.bw_up_internal", fmt.Sprintf("%d", req.BwUpInternal)},
		{"beam.bw_down_internal", fmt.Sprintf("%d", req.BwDownInternal)},
		{"beam.bw_up_external", fmt.Sprintf("%d", req.BwUpExternal)},
		{"beam.bw_down_external", fmt.Sprintf("%d", req.BwDownExternal)},

		// Operator-recorded host hardware references (informational only).
		{"beam.ref_up_internal", fmt.Sprintf("%d", req.RefUpInternal)},
		{"beam.ref_down_internal", fmt.Sprintf("%d", req.RefDownInternal)},
		{"beam.ref_up_external", fmt.Sprintf("%d", req.RefUpExternal)},
		{"beam.ref_down_external", fmt.Sprintf("%d", req.RefDownExternal)},

		// Beam upload limits (bytes, 0 = unlimited), enforced node-side.
		{"beam.max_upload_bytes", fmt.Sprintf("%d", req.MaxUploadBytes)},
		{"beam.daily_upload_bytes", fmt.Sprintf("%d", req.DailyUploadBytes)},
	}
	for _, p := range pairs {
		if err := h.state.Store.SetSetting(p.k, p.v); err != nil {
			sendJSONError(w, "Failed to save", http.StatusInternalServerError)
			return
		}
	}

	// Publish to Redis so Nodes (per-direction internal caps) and the
	// Relay (per-direction external caps) can pick up changes without a
	// restart. The legacy beam:bw_limit key stays for back-compat with
	// older nodes that don't read the split keys yet.
	if h.state.Redis != nil {
		ctx := r.Context()
		h.state.Redis.Set(ctx, "beam:bw_limit", fmt.Sprintf("%d", effectiveBw), 0)
		h.state.Redis.Set(ctx, "beam:bw_up_internal", fmt.Sprintf("%d", req.BwUpInternal), 0)
		h.state.Redis.Set(ctx, "beam:bw_down_internal", fmt.Sprintf("%d", req.BwDownInternal), 0)
		h.state.Redis.Set(ctx, "beam:bw_up_external", fmt.Sprintf("%d", req.BwUpExternal), 0)
		h.state.Redis.Set(ctx, "beam:bw_down_external", fmt.Sprintf("%d", req.BwDownExternal), 0)
		h.state.Redis.Set(ctx, "beam:max_upload_bytes", fmt.Sprintf("%d", req.MaxUploadBytes), 0)
		h.state.Redis.Set(ctx, "beam:daily_upload_bytes", fmt.Sprintf("%d", req.DailyUploadBytes), 0)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Beam settings saved",
	})
}

// LoadBeamSettings loads Beam settings from DB with defaults.
func (h *SettingsHandler) LoadBeamSettings() BeamSettings {
	getSetting := func(key string) string {
		val, _ := h.state.Store.GetSetting(key)
		return val
	}

	manualOverride := getSetting("beam.relay_address")
	publicHost := getSetting("beam.public_host")
	effective, _ := resolveRelay(context.Background(), h.state.Redis, manualOverride, publicHost)

	minVersionMode := getSetting("beam.min_version_mode")
	if minVersionMode != beamMinVersionModeAuto {
		minVersionMode = beamMinVersionModeManual // default + normalize any legacy/blank value
	}

	devChannelAccess := getSetting("beam.dev_channel_access")
	if devChannelAccess != beamDevAccessAdminsOnly && devChannelAccess != beamDevAccessAllUsers {
		devChannelAccess = beamDevAccessDisabled // default + normalize any legacy/blank value
	}

	settings := BeamSettings{
		RelayAddress:     effective,
		ManualOverride:   manualOverride,
		PublicHost:       publicHost,
		DownloadLink:     getSetting("beam.download_link"),
		MinVersion:       getSetting("beam.min_version"),
		MinVersionMode:   minVersionMode,
		DevChannelAccess: devChannelAccess,
		Enabled:          true,
	}

	enabledStr := getSetting("beam.enabled")
	if enabledStr == "false" {
		settings.Enabled = false
	}

	if val := getSetting("beam.bw_limit"); val != "" {
		fmt.Sscanf(val, "%d", &settings.BwLimit)
	}

	// Per-direction throttle splits + operator-recorded references.
	// Defaults stay 0 so they round-trip cleanly when never set.
	fmt.Sscanf(getSetting("beam.bw_up_internal"), "%d", &settings.BwUpInternal)
	fmt.Sscanf(getSetting("beam.bw_down_internal"), "%d", &settings.BwDownInternal)
	fmt.Sscanf(getSetting("beam.bw_up_external"), "%d", &settings.BwUpExternal)
	fmt.Sscanf(getSetting("beam.bw_down_external"), "%d", &settings.BwDownExternal)
	fmt.Sscanf(getSetting("beam.ref_up_internal"), "%d", &settings.RefUpInternal)
	fmt.Sscanf(getSetting("beam.ref_down_internal"), "%d", &settings.RefDownInternal)
	fmt.Sscanf(getSetting("beam.ref_up_external"), "%d", &settings.RefUpExternal)
	fmt.Sscanf(getSetting("beam.ref_down_external"), "%d", &settings.RefDownExternal)

	// Beam upload limits (bytes). Default 0 (unlimited) when never set.
	fmt.Sscanf(getSetting("beam.max_upload_bytes"), "%d", &settings.MaxUploadBytes)
	fmt.Sscanf(getSetting("beam.daily_upload_bytes"), "%d", &settings.DailyUploadBytes)

	return settings
}

// minNonZeroInt64 returns the smaller of two values, treating 0 as
// "unlimited" so it never wins over a real cap. Used to fold the two
// internal-direction throttle splits into a single legacy bw_limit
// until per-direction enforcement ships.
func minNonZeroInt64(a, b int64) int64 {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// --- Backup Settings ---

// BackupConfig captures the GLOBAL knobs that decide which kind of backup
// storage the panel will let users pick, plus the per-server quota policy.
// Per-instance credentials (S3 keys, NFS paths) stay in the backup_storages
// table; this struct only governs which provider rows are usable and how
// large each server's backup folder is allowed to get on node-local hosts.
type BackupConfig struct {
	// Mode is one of "s3", "node-local", "shared", or "core-storage". It picks which
	// backup_storages rows (by provider) the UI exposes as creatable, and
	// — for node-local — turns on the quota fields below.
	Mode string `json:"mode"`

	// QuotaPerServerGB is the per-server hard cap on the .dylaris-backups/
	// folder when Mode == "node-local". 0 means unlimited. The cap is
	// enforced at the application layer (Core checks current usage before
	// approving a new backup); filesystem-level enforcement via XFS project
	// quotas is intentionally out of scope for this round.
	QuotaPerServerGB int `json:"quotaPerServerGb"`

	// ShareQuotaWithServer folds the backup folder into the same quota
	// the server's container storage uses, instead of accounting for it
	// separately. Useful when ops doesn't want two quotas to monitor.
	// Only honored when Mode == "node-local"; ignored otherwise.
	ShareQuotaWithServer bool `json:"shareQuotaWithServer"`
}

var defaultBackupConfig = BackupConfig{
	Mode:                 "shared",
	QuotaPerServerGB:     10,
	ShareQuotaWithServer: false,
}

// validBackupMode keeps the accepted modes literal - adding a new one has to
// be a conscious change here AND in the storage factory. "core-storage" was
// added rather than exposing that provider regardless of Mode: Mode's
// documented job is to describe what the panel offers, and bypassing it would
// falsify its own comment. The default stays "shared", so no existing install
// shifts.
func validBackupMode(m string) bool {
	return m == "s3" || m == "node-local" || m == "shared" || m == "core-storage"
}

// GetBackupConfig GET /api/settings/backup - PANEL settings.read (RequireCap at the route).
func (h *SettingsHandler) GetBackupConfig(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": h.LoadBackupConfig(),
	})
}

// SaveBackupConfig POST /api/settings/backup - PANEL settings.write (RequireCap at the route).
func (h *SettingsHandler) SaveBackupConfig(w http.ResponseWriter, r *http.Request) {
	var req BackupConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if !validBackupMode(req.Mode) {
		sendJSONError(w, "Invalid backup mode (expected s3, node-local, shared, or core-storage)", http.StatusBadRequest)
		return
	}
	if req.QuotaPerServerGB < 0 {
		req.QuotaPerServerGB = 0
	}

	pairs := []struct{ k, v string }{
		{"backup.mode", req.Mode},
		{"backup.quota_per_server_gb", fmt.Sprintf("%d", req.QuotaPerServerGB)},
		{"backup.share_quota_with_server", fmt.Sprintf("%t", req.ShareQuotaWithServer)},
	}
	for _, p := range pairs {
		if err := h.state.Store.SetSetting(p.k, p.v); err != nil {
			sendJSONError(w, "Failed to save setting: "+p.k, http.StatusInternalServerError)
			return
		}
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// LoadBackupConfig reads the persisted BackupConfig, returning defaults
// for any missing keys so the panel always has something usable to render.
func (h *SettingsHandler) LoadBackupConfig() BackupConfig {
	cfg := defaultBackupConfig
	if v, _ := h.state.Store.GetSetting("backup.mode"); v != "" && validBackupMode(v) {
		cfg.Mode = v
	}
	if v, _ := h.state.Store.GetSetting("backup.quota_per_server_gb"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 {
			cfg.QuotaPerServerGB = n
		}
	}
	if v, _ := h.state.Store.GetSetting("backup.share_quota_with_server"); v != "" {
		cfg.ShareQuotaWithServer = v == "true"
	}
	return cfg
}

// --- Routing Mode Settings ---

type RoutingModeSettings struct {
	Mode     string `json:"mode"`     // "ip_port" | "both" | "gateway"
	FileMode string `json:"fileMode"` // "sftp" | "both" | "beam"
}

func validRoutingMode(v string) bool {
	return v == "ip_port" || v == "both" || v == "gateway"
}

func validFileMode(v string) bool {
	return v == "sftp" || v == "both" || v == "beam"
}

// GetRoutingMode GET /api/settings/routing-mode — available to all authenticated users
func (h *SettingsHandler) GetRoutingMode(w http.ResponseWriter, r *http.Request) {
	mode, _ := h.state.Store.GetSetting("routing_mode")
	fileMode, _ := h.state.Store.GetSetting("file_access_mode")
	if mode == "" {
		mode = "ip_port"
	}
	if fileMode == "" {
		fileMode = "sftp"
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"mode":     mode,
		"fileMode": fileMode,
	})
}

// SaveRoutingMode POST /api/settings/routing-mode - PANEL settings.write (RequireCap at the route)
func (h *SettingsHandler) SaveRoutingMode(w http.ResponseWriter, r *http.Request) {
	var req RoutingModeSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if !validRoutingMode(req.Mode) {
		sendJSONError(w, "Invalid mode: must be ip_port, both, or gateway", http.StatusBadRequest)
		return
	}
	if !validFileMode(req.FileMode) {
		sendJSONError(w, "Invalid fileMode: must be sftp, both, or beam", http.StatusBadRequest)
		return
	}

	if err := h.state.Store.SetSetting("routing_mode", req.Mode); err != nil {
		sendJSONError(w, "Failed to save routing_mode", http.StatusInternalServerError)
		return
	}
	if err := h.state.Store.SetSetting("file_access_mode", req.FileMode); err != nil {
		sendJSONError(w, "Failed to save file_access_mode", http.StatusInternalServerError)
		return
	}

	// Publish to Redis so Nodes pick it up within 30s
	ctx := r.Context()
	h.state.Redis.Set(ctx, "dylaris:routing_mode", req.Mode, 0)
	h.state.Redis.Set(ctx, "dylaris:file_access_mode", req.FileMode, 0)

	// Gateway off => auto-move cannot run (a moved server would lose its
	// stable address), so disable the feature and clear every per-server
	// opt-in. Best-effort: a failure here must not abort the routing switch.
	if req.Mode == "ip_port" {
		if err := h.state.Store.SetSetting("feature_auto_move_enabled", "false"); err != nil {
			log.Printf("routing-mode: failed to disable auto-move feature flag: %v", err)
		}
		h.state.FeatureFlags.Invalidate("feature_auto_move_enabled")
		if err := h.state.Store.ResetAllAutoMove(); err != nil {
			log.Printf("routing-mode: failed to reset per-server auto-move opt-ins: %v", err)
		}
	}

	// Kick off migration
	queued := 0
	if h.state.RoutingMigration != nil {
		n, err := h.state.RoutingMigration.Run(ctx, req.Mode)
		if err == nil {
			queued = n
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"serversQueued": queued,
	})
}

// --- Warp Spoke Firewall Settings ---

// WarpFirewallRedisKey is the fixed central-Redis key the warp leaders read and
// poll for the admin-configured spoke destination-port allowlist. MUST stay
// byte-identical to gateway/warp firewallAllowedPortsKey.
const WarpFirewallRedisKey = "dylaris:warp:firewall:allowed_ports"

// defaultWarpSpokeAllowedPorts is the compiled-in default allowlist (6379 Redis,
// 25501 Core gRPC, 25551 beam relay, 25560 edge tunnel). Written in the sorted
// order normalizeWarpPorts emits, so the first-boot value does not visibly
// reorder on the first save. MUST match gateway/warp defaultSpokeAllowedPorts.
const defaultWarpSpokeAllowedPorts = "6379,25501,25551,25560"

type WarpFirewallSettings struct {
	AllowedPorts string `json:"allowedPorts"` // comma-separated destination TCP ports the overlay leaders allow spokes to reach
}

// normalizeWarpPorts validates and normalizes a comma-separated port list into a
// sorted, deduped CSV. It rejects any non-numeric or out-of-range (1..65535)
// entry so a bad value never reaches the leaders (which trust this key).
func normalizeWarpPorts(csv string) (string, error) {
	seen := map[int]bool{}
	var ports []int
	for _, part := range strings.Split(csv, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return "", fmt.Errorf("invalid port %q", p)
		}
		if n < 1 || n > 65535 {
			return "", fmt.Errorf("port %d out of range 1-65535", n)
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		ports = append(ports, n)
	}
	sort.Ints(ports)
	strs := make([]string, len(ports))
	for i, p := range ports {
		strs[i] = strconv.Itoa(p)
	}
	return strings.Join(strs, ","), nil
}

// LoadWarpSpokeAllowedPorts returns the persisted allowlist, or the compiled-in
// default when unset. The stored value is already normalized on save.
func (h *SettingsHandler) LoadWarpSpokeAllowedPorts() string {
	v, _ := h.state.Store.GetSetting("warp_spoke_allowed_ports")
	if v == "" {
		return defaultWarpSpokeAllowedPorts
	}
	return v
}

// GetWarpFirewallSettings GET /api/settings/warp-firewall - PANEL settings.read (RequireCap at the route).
func (h *SettingsHandler) GetWarpFirewallSettings(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": WarpFirewallSettings{AllowedPorts: h.LoadWarpSpokeAllowedPorts()},
	})
}

// SaveWarpFirewallSettings POST /api/settings/warp-firewall - PANEL settings.write
// (RequireCap at the route). Validates + normalizes the port list, persists it,
// and publishes it to the central-Redis key the warp leaders poll. Requires at
// least one port: an empty allowlist would silently lock every spoke out of all
// internal services.
func (h *SettingsHandler) SaveWarpFirewallSettings(w http.ResponseWriter, r *http.Request) {
	var req WarpFirewallSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	norm, err := normalizeWarpPorts(req.AllowedPorts)
	if err != nil {
		sendJSONError(w, "Invalid ports: "+err.Error(), http.StatusBadRequest)
		return
	}
	if norm == "" {
		sendJSONError(w, "At least one port is required", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.SetSetting("warp_spoke_allowed_ports", norm); err != nil {
		sendJSONError(w, "Failed to save setting", http.StatusInternalServerError)
		return
	}
	propagated := true
	if h.state.Redis != nil {
		if rerr := h.state.Redis.Set(r.Context(), WarpFirewallRedisKey, norm, 0).Err(); rerr != nil {
			log.Printf("warp-firewall: failed to publish allowlist to Redis (leaders keep the stale value until the next successful save): %v", rerr)
			propagated = false
		}
	}
	resp := map[string]interface{}{
		"success":  true,
		"settings": WarpFirewallSettings{AllowedPorts: norm},
	}
	if !propagated {
		// The Postgres row IS saved (source of truth for a later successful
		// publish), but the warp leaders poll a fixed Redis key with no other
		// reconcile path, so a failed publish leaves the OLD, wider allowlist
		// live. Tell the admin instead of a bare success - silently pretending
		// this succeeded would hide a firewall rule that never tightened.
		resp["success"] = false
		resp["error"] = "Saved, but failed to publish to the warp leaders. The previous allowlist is still active; retry the save."
	}
	json.NewEncoder(w).Encode(resp)
}
