package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
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
	ProxyEnabled bool `json:"proxyEnabled"`
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

	if err := h.state.Store.SetSetting("feature_proxy_enabled", proxyVal); err != nil {
		sendJSONError(w, "Failed to save setting", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// LoadFeatureSettings reads feature flags from the database. Available to all authenticated users.
func (h *SettingsHandler) LoadFeatureSettings() FeatureSettings {
	proxyVal, _ := h.state.Store.GetSetting("feature_proxy_enabled")
	return FeatureSettings{
		ProxyEnabled: proxyVal != "false", // default true
	}
}

// --- Gateway Settings ---

type GatewaySettings struct {
	RedisMode        string        `json:"redisMode"` // "shared" or "separate"
	RedisAddr        string        `json:"redisAddr"`
	RedisUser        string        `json:"redisUser"`
	RedisPass        string        `json:"redisPass,omitempty"`
	RedisDb          int           `json:"redisDb"`
	DefaultLinkImage string        `json:"defaultLinkImage"`
	Limits           GatewayLimits `json:"limits"`
}

type GatewayLimits struct {
	Global           int  `json:"global"`
	UserDefault      int  `json:"userDefault"`
	PerServer        int  `json:"perServer"`
	PortMc           int  `json:"portMc"`
	PortMcEnabled    bool `json:"portMcEnabled"`
	PortHttps        int  `json:"portHttps"`
	PortHttpsEnabled bool `json:"portHttpsEnabled"`
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
	getInt := func(key string) int {
		val, _ := h.state.Store.GetSetting(key)
		n, _ := fmt.Sscanf(val, "%d", new(int))
		if n == 0 {
			return 0
		}
		var v int
		fmt.Sscanf(val, "%d", &v)
		return v
	}

	getLimit := func(scope string) int {
		l, err := h.state.Store.GetGatewayRouteLimit(scope)
		if err != nil {
			return 0
		}
		return l.MaxRoutes
	}

	redisMode := getSetting("gateway_redis_mode")
	if redisMode == "" {
		redisMode = "shared"
	}

	settings := GatewaySettings{
		RedisMode:        redisMode,
		RedisAddr:        getSetting("gateway_redis_addr"),
		RedisUser:        getSetting("gateway_redis_user"),
		// RedisPass intentionally omitted from response
		RedisDb:          getInt("gateway_redis_db"),
		DefaultLinkImage: getSetting("gateway_link_image"),
		Limits: GatewayLimits{
			Global:           getLimit("global"),
			UserDefault:      getLimit("user_default"),
			PerServer:        getLimit("per_server"),
			PortMc:           getLimit("port:25565"),
			PortMcEnabled:    getSetting("gateway_port_mc_enabled") != "false",
			PortHttps:        getLimit("port:443"),
			PortHttpsEnabled: getSetting("gateway_port_https_enabled") != "false",
		},
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": settings,
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

	redisMode := req.RedisMode
	if redisMode == "" {
		redisMode = "shared"
	}

	pairs := []struct{ k, v string }{
		{"gateway_redis_mode", redisMode},
		{"gateway_redis_addr", req.RedisAddr},
		{"gateway_redis_user", req.RedisUser},
		{"gateway_redis_db", fmt.Sprintf("%d", req.RedisDb)},
		{"gateway_link_image", req.DefaultLinkImage},
	}
	// Only save password if non-empty (don't overwrite with blank)
	if req.RedisPass != "" {
		pairs = append(pairs, struct{ k, v string }{"gateway_redis_pass", req.RedisPass})
	}
	for _, p := range pairs {
		if err := h.state.Store.SetSetting(p.k, p.v); err != nil {
			sendJSONError(w, "Failed to save setting: "+p.k, http.StatusInternalServerError)
			return
		}
	}

	// Save port-enable settings
	portSettings := []struct{ k, v string }{
		{"gateway_port_mc_enabled", fmt.Sprintf("%t", req.Limits.PortMcEnabled)},
		{"gateway_port_https_enabled", fmt.Sprintf("%t", req.Limits.PortHttpsEnabled)},
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
	}
	for _, l := range limits {
		if err := h.state.Store.SetGatewayRouteLimit(l.scope, l.max); err != nil {
			sendJSONError(w, "Failed to save limit: "+l.scope, http.StatusInternalServerError)
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
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
	RelayAddress string `json:"relayAddress"` // Public BeamRelay address for clients
	BwLimit      int64  `json:"bwLimit"`      // Bytes/sec, 0 = unlimited
	Enabled      bool   `json:"enabled"`
}

// GetBeamSettings GET /api/settings/beam
func (h *SettingsHandler) GetBeamSettings(w http.ResponseWriter, r *http.Request) {
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	settings := h.LoadBeamSettings()
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

	pairs := []struct{ k, v string }{
		{"beam.relay_address", req.RelayAddress},
		{"beam.bw_limit", fmt.Sprintf("%d", req.BwLimit)},
		{"beam.enabled", enabledStr},
	}
	for _, p := range pairs {
		if err := h.state.Store.SetSetting(p.k, p.v); err != nil {
			sendJSONError(w, "Failed to save", http.StatusInternalServerError)
			return
		}
	}

	// Publish bw_limit to Redis so Nodes can pick it up
	if h.state.Redis != nil {
		h.state.Redis.Set(r.Context(), "beam:bw_limit", fmt.Sprintf("%d", req.BwLimit), 0)
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

	settings := BeamSettings{
		RelayAddress: getSetting("beam.relay_address"),
		Enabled:      true,
	}

	enabledStr := getSetting("beam.enabled")
	if enabledStr == "false" {
		settings.Enabled = false
	}

	if val := getSetting("beam.bw_limit"); val != "" {
		fmt.Sscanf(val, "%d", &settings.BwLimit)
	}

	return settings
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
