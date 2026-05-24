package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type SettingsHandler struct {
	state          *AppState
	libraryHandler *LibraryHandler
}

func NewSettingsHandler(state *AppState, lh *LibraryHandler) *SettingsHandler {
	return &SettingsHandler{state: state, libraryHandler: lh}
}

type LibrarySettings struct {
	Type        string `json:"type"`        // "local", "s3"
	Path        string `json:"path"`        // for local
	S3Endpoint  string `json:"s3Endpoint"`
	S3Bucket    string `json:"s3Bucket"`
	S3Region    string `json:"s3Region"`
	S3AccessKey string `json:"s3AccessKey"`
	S3SecretKey string `json:"s3SecretKey,omitempty"` // omit in GET response
}

// GetLibrarySettings GET /api/settings/library
func (h *SettingsHandler) GetLibrarySettings(w http.ResponseWriter, r *http.Request) {
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	getSetting := func(key string) string {
		val, _ := h.state.Store.GetSetting(key)
		return val
	}

	settings := LibrarySettings{
		Type:        getSetting("library_type"),
		Path:        getSetting("library_path"),
		S3Endpoint:  getSetting("library_s3_endpoint"),
		S3Bucket:    getSetting("library_s3_bucket"),
		S3Region:    getSetting("library_s3_region"),
		S3AccessKey: getSetting("library_s3_access_key"),
		// S3SecretKey intentionally omitted from response
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": settings,
	})
}

// SaveLibrarySettings POST /api/settings/library
func (h *SettingsHandler) SaveLibrarySettings(w http.ResponseWriter, r *http.Request) {
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	var req LibrarySettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	pairs := []struct{ k, v string }{
		{"library_type", req.Type},
		{"library_path", req.Path},
		{"library_s3_endpoint", req.S3Endpoint},
		{"library_s3_bucket", req.S3Bucket},
		{"library_s3_region", req.S3Region},
		{"library_s3_access_key", req.S3AccessKey},
	}
	if req.S3SecretKey != "" {
		pairs = append(pairs, struct{ k, v string }{"library_s3_secret_key", req.S3SecretKey})
	}

	for _, p := range pairs {
		if err := h.state.Store.SetSetting(p.k, p.v); err != nil {
			sendJSONError(w, "Failed to save setting: "+p.k, http.StatusInternalServerError)
			return
		}
	}

	// Rebuild provider with the new settings
	if h.libraryHandler != nil {
		h.libraryHandler.RefreshProvider()
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// --- File Manager Settings ---

type FileManagerSettings struct {
	AdminUploadLimit  int64 `json:"adminUploadLimit"`  // bytes
	AdminDownloadLimit int64 `json:"adminDownloadLimit"` // bytes
	UserUploadLimit   int64 `json:"userUploadLimit"`   // bytes
	UserDownloadLimit int64 `json:"userDownloadLimit"` // bytes
}

var defaultFileManagerSettings = FileManagerSettings{
	AdminUploadLimit:   2 * 1024 * 1024 * 1024,  // 2 GB
	AdminDownloadLimit: 5 * 1024 * 1024 * 1024,  // 5 GB
	UserUploadLimit:    500 * 1024 * 1024,         // 500 MB
	UserDownloadLimit:  1 * 1024 * 1024 * 1024,   // 1 GB
}

// GetFileManagerSettings GET /api/settings/filemanager
func (h *SettingsHandler) GetFileManagerSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	settings := h.loadFileManagerSettings()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": settings,
	})
}

// SaveFileManagerSettings POST /api/settings/filemanager
func (h *SettingsHandler) SaveFileManagerSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

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

// SaveFeatureSettings POST /api/settings/features
func (h *SettingsHandler) SaveFeatureSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

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
}

type GatewayLimits struct {
	Global           int  `json:"global"`
	UserDefault      int  `json:"userDefault"`
	PerServer        int  `json:"perServer"`
	PortMc           int  `json:"portMc"`
	PortMcEnabled    bool `json:"portMcEnabled"`
	PortHttps        int  `json:"portHttps"`
	PortHttpsEnabled bool `json:"portHttpsEnabled"`
	PortHttp         int  `json:"portHttp"`
	PortHttpEnabled  bool `json:"portHttpEnabled"`
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

// GetGatewaySettings GET /api/settings/gateway
func (h *SettingsHandler) GetGatewaySettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

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
			Global:           getLimit("global"),
			UserDefault:      getLimit("user_default"),
			PerServer:        getLimit("per_server"),
			PortMc:           getLimit("port:25565"),
			PortMcEnabled:    getSetting("gateway_port_mc_enabled") != "false",
			PortHttps:        getLimit("port:443"),
			PortHttpsEnabled: getSetting("gateway_port_https_enabled") != "false",
			PortHttp:         getLimit("port:80"),
			PortHttpEnabled:  getSetting("gateway_port_http_enabled") == "true",
		},
		HosterDomains:        h.loadHosterDomains(),
		CustomDomainsEnabled: getSetting("gateway_custom_domains_enabled") == "true",
		CnameTarget:          getSetting("gateway_cname_target"),
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

// SaveGatewaySettings POST /api/settings/gateway
func (h *SettingsHandler) SaveGatewaySettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

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

	// Save port-enable settings
	portSettings := []struct{ k, v string }{
		{"gateway_port_mc_enabled", fmt.Sprintf("%t", req.Limits.PortMcEnabled)},
		{"gateway_port_https_enabled", fmt.Sprintf("%t", req.Limits.PortHttpsEnabled)},
		{"gateway_port_http_enabled", fmt.Sprintf("%t", req.Limits.PortHttpEnabled)},
		{"gateway_hoster_domains", string(hostersJSON)},
		{"gateway_custom_domains_enabled", fmt.Sprintf("%t", req.CustomDomainsEnabled)},
		{"gateway_cname_target", strings.TrimSpace(req.CnameTarget)},
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
		{"port:443", req.Limits.PortHttps},
		{"port:80", req.Limits.PortHttp},
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
}

var defaultPlacementSettings = PlacementSettings{
	CPUOvercommitDefault: 2.0,  // CPU is time-shared, 2.0x = 200% is conservative
	RAMOvercommitDefault: 1.0,  // RAM has no default overcommit (safer); 100%
	DiskBufferGB:         10,
	RebalanceEnabled:     false,
	RebalanceThreshold:   90,
	PortMode:             "sequential",
	ContainerPort:        25565,
}

// GetPlacementSettings GET /api/settings/placement
func (h *SettingsHandler) GetPlacementSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	s := h.LoadPlacementSettings()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": s,
	})
}

// SavePlacementSettings POST /api/settings/placement
func (h *SettingsHandler) SavePlacementSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
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

	pairs := []struct{ k, v string }{
		{"placement.cpu_overcommit_default", fmt.Sprintf("%g", req.CPUOvercommitDefault)},
		{"placement.ram_overcommit_default", fmt.Sprintf("%g", req.RAMOvercommitDefault)},
		{"placement.disk_buffer_gb", fmt.Sprintf("%d", req.DiskBufferGB)},
		{"placement.rebalance_enabled", fmt.Sprintf("%t", req.RebalanceEnabled)},
		{"placement.rebalance_threshold", fmt.Sprintf("%d", req.RebalanceThreshold)},
		{"placement.port_mode", req.PortMode},
		{"placement.container_port", fmt.Sprintf("%d", req.ContainerPort)},
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
	return s
}

// --- Server Settings ---

type ServerSettings struct {
	MaxSubServers int `json:"maxSubServers"` // 0 = unlimited
}

var defaultServerSettings = ServerSettings{
	MaxSubServers: 3,
}

// GetServerSettings GET /api/settings/servers
func (h *SettingsHandler) GetServerSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	settings := h.LoadServerSettings()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": settings,
	})
}

// SaveServerSettings POST /api/settings/servers
func (h *SettingsHandler) SaveServerSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

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

// TestLibraryConnection GET /api/settings/library/test
func (h *SettingsHandler) TestLibraryConnection(w http.ResponseWriter, r *http.Request) {
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	libType, _ := h.state.Store.GetSetting("library_type")
	libPath, _ := h.state.Store.GetSetting("library_path")

	var ok bool
	var message string

	switch libType {
	case "s3":
		// Placeholder: S3 connection test would go here
		ok = false
		message = "S3 connection test not yet implemented. Configure and save to use S3 storage."
	default:
		// Local: Is directory reachable?
		if libPath == "" {
			ok = true
			message = "Using default library path (dylaris_data/library)"
		} else {
			_, err := h.state.Store.GetSetting("library_path")
			if err != nil && err != sql.ErrNoRows {
				ok = false
				message = "Database error"
			} else {
				ok = true
				message = "Local path configured: " + libPath
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"ok":      ok,
		"message": message,
	})
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
	BwLimit      int64 `json:"bwLimit"`
	Enabled      bool  `json:"enabled"`
	DownloadLink string `json:"downloadLink"` // Optional CDN URL — overrides relay-served download

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

// SaveBeamSettings POST /api/settings/beam
func (h *SettingsHandler) SaveBeamSettings(w http.ResponseWriter, r *http.Request) {
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	var req BeamSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
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
	}
	for _, p := range pairs {
		if err := h.state.Store.SetSetting(p.k, p.v); err != nil {
			sendJSONError(w, "Failed to save", http.StatusInternalServerError)
			return
		}
	}

	// Publish the effective bw_limit to Redis so Nodes can pick it up.
	if h.state.Redis != nil {
		h.state.Redis.Set(r.Context(), "beam:bw_limit", fmt.Sprintf("%d", effectiveBw), 0)
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

	settings := BeamSettings{
		RelayAddress:   effective,
		ManualOverride: manualOverride,
		PublicHost:     publicHost,
		DownloadLink:   getSetting("beam.download_link"),
		Enabled:        true,
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

// SaveRoutingMode POST /api/settings/routing-mode — admin only
func (h *SettingsHandler) SaveRoutingMode(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

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

	// Kick off migration
	queued := 0
	if h.state.RoutingMigration != nil {
		n, err := h.state.RoutingMigration.Run(ctx, req.Mode)
		if err == nil {
			queued = n
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"serversQueued":  queued,
	})
}
