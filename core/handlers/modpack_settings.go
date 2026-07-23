package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type ModpackSettingsHandler struct {
	state *AppState
}

func NewModpackSettingsHandler(state *AppState) *ModpackSettingsHandler {
	return &ModpackSettingsHandler{state: state}
}

// modpackSettings is the wire shape used by GET + PUT. The s3 secret is
// write-only from the panel's perspective: GET omits it (returns the empty
// string) so it never lands in the panel state or browser devtools.
type modpackSettings struct {
	FeatureEnabled           bool     `json:"featureEnabled"`
	Provider                 string   `json:"provider"` // "local" | "s3"
	Paths                    []string `json:"paths"`    // local: absolute paths
	S3Endpoint               string   `json:"s3Endpoint"`
	S3Bucket                 string   `json:"s3Bucket"`
	S3Region                 string   `json:"s3Region"`
	S3AccessKey              string   `json:"s3AccessKey"`
	S3SecretKey              string   `json:"s3SecretKey,omitempty"` // write-only
	UpdateCheckIntervalHours int      `json:"updateCheckIntervalHours"`
	ShareLinksEnabled        bool     `json:"shareLinksEnabled"`
	// ConnectionID references a saved storage connection. When non-zero, modpack
	// storage is built from that connection (buildModpackStorageProvider) and the
	// inline s3 fields are ignored. 0 = use the inline config.
	ConnectionID int `json:"connectionId"`
}

// Get GET /api/admin/settings/modpacks - PANEL settings.read (RequireCap at the route).
func (h *ModpackSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	get := func(k string) string {
		v, _ := h.state.Store.GetSetting(k)
		return v
	}
	out := modpackSettings{
		FeatureEnabled: get("feature_modpacks_enabled") == "true",
		Provider:       get("modpack_storage_provider"),
		S3Endpoint:     get("modpack_storage_s3_endpoint"),
		S3Bucket:       get("modpack_storage_s3_bucket"),
		S3Region:       get("modpack_storage_s3_region"),
		S3AccessKey:    get("modpack_storage_s3_access_key"),
		// Secret intentionally omitted on GET.
	}
	if out.Provider == "" {
		out.Provider = "local"
	}
	rawPaths := get("modpack_storage_paths")
	if rawPaths != "" {
		_ = json.Unmarshal([]byte(rawPaths), &out.Paths)
	}
	if out.Paths == nil {
		out.Paths = []string{}
	}
	interval := 24
	if v := get("modpack_update_check_interval_hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = n
		}
	}
	out.UpdateCheckIntervalHours = interval
	out.ShareLinksEnabled = get("modpack_share_links_enabled") == "true"
	out.ConnectionID, _ = strconv.Atoi(get(keyModpackStorageConnectionID)) // "" or bad -> 0 = none
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": out,
		// secretSet tells the panel whether to render "(unchanged — leave
		// empty)" vs "(not set yet)" without ever exposing the value.
		"secretSet": get("modpack_storage_s3_secret_key") != "",
	})
}

// validModpackProvider mirrors the switch in storage/modpack/factory.go's
// NewProviderFromSettings. Without this allowlist a typo (or an
// aspirationally-named future backend) would persist to
// modpack_storage_provider and only fail much later when a pack is actually
// read or written.
//
// NOTE: this is an input allowlist, not an authorization boundary, same as
// validBackupProvider in backup.go.
func validModpackProvider(p string) bool {
	switch p {
	case "local", "s3", "core-storage":
		return true
	}
	return false
}

// Set PUT /api/admin/settings/modpacks
//
// Body: full modpackSettings. Empty S3SecretKey means "don't change", so the
// admin can update other fields without re-entering the secret. To clear the
// secret, send "" with a special sentinel? No — YAGNI; admin can rotate or
// delete via DB if needed.
func (h *ModpackSettingsHandler) Set(w http.ResponseWriter, r *http.Request) {
	var req modpackSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "local"
	}
	if !validModpackProvider(req.Provider) {
		sendJSONError(w, "provider must be local, s3 or core-storage", http.StatusBadRequest)
		return
	}
	// Checked for every provider, not only "s3": the writes below persist the
	// endpoint whatever the provider is, Get echoes it back, and
	// modpackBackendLabel reads it straight out of the settings table. Gating
	// this on req.Provider == "s3" would leave a save with provider "local" as
	// an open path for the same credential.
	if err := validateS3Endpoint("modpacks", req.S3Endpoint); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Normalize paths: strip empties + dedupe + MkdirAll so the provider
	// doesn't fail on first Put. We deliberately do NOT validate writability
	// (no Test-write here) — admin can verify out-of-band.
	cleaned := make([]string, 0, len(req.Paths))
	seen := map[string]bool{}
	for _, p := range req.Paths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		_ = os.MkdirAll(p, 0o755) // best-effort; surface failure on first Put
		cleaned = append(cleaned, p)
	}
	pathsJSON, _ := json.Marshal(cleaned)

	uch := req.UpdateCheckIntervalHours
	if uch <= 0 {
		uch = 24
	}

	writes := []struct{ k, v string }{
		{"feature_modpacks_enabled", boolStr(req.FeatureEnabled)},
		{"modpack_storage_provider", req.Provider},
		{"modpack_storage_paths", string(pathsJSON)},
		{"modpack_storage_s3_endpoint", req.S3Endpoint},
		{"modpack_storage_s3_bucket", req.S3Bucket},
		{"modpack_storage_s3_region", req.S3Region},
		{"modpack_storage_s3_access_key", req.S3AccessKey},
		{"modpack_update_check_interval_hours", strconv.Itoa(uch)},
		{"modpack_share_links_enabled", boolStr(req.ShareLinksEnabled)},
		{keyModpackStorageConnectionID, storageConnIDSetting(req.ConnectionID)},
	}
	for _, kv := range writes {
		if err := h.state.Store.SetSetting(kv.k, kv.v); err != nil {
			sendJSONError(w, "Save failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if req.S3SecretKey != "" {
		if err := h.state.Store.SetSetting("modpack_storage_s3_secret_key", req.S3SecretKey); err != nil {
			sendJSONError(w, "Save failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Invalidate the cached feature flag so subsequent reads in this Core
	// pick up the new value instantly; cross-Core staleness is bounded by
	// the 60s cache TTL.
	h.state.FeatureFlags.Invalidate("feature_modpacks_enabled")
	h.state.FeatureFlags.Invalidate("modpack_share_links_enabled")

	// Publish events: features.changed re-renders the panel banner/gating;
	// modpack_settings.changed wakes the Settings → Modpacks tab.
	h.state.Events.Publish(r.Context(), "features.changed", map[string]interface{}{"feature": "modpacks", "enabled": req.FeatureEnabled})
	h.state.Events.Publish(r.Context(), "modpack_settings.changed", nil)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// --- Per-user modpack flag ---

type modpackUserFlagRequest struct {
	CanCreate bool `json:"canCreate"`
}

// SetUserFlag PATCH /api/admin/users/{id}/modpack-flag - PANEL settings.write
// (RequireCap at the route).
func (h *ModpackSettingsHandler) SetUserFlag(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUserID(w, r)
	if !ok {
		return
	}
	var req modpackUserFlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.SetUserCanCreateModpacks(userID, req.CanCreate); err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "users.changed", map[string]interface{}{"userId": userID})
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
