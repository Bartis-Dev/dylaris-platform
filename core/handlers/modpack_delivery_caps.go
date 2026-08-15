package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"dylaris-core/services"
)

// deliveryCapabilities tells the panel which Solder delivery modes are usable
// with the CURRENTLY SAVED storage config, so it can grey out the rest.
type deliveryCapabilities struct {
	CanPresign       bool              `json:"canPresign"`
	PublicConfigured bool              `json:"publicConfigured"`
	PublicReachable  *bool             `json:"publicReachable"`
	// PrivatePackCount is how many Solder-capable packs are private/hidden; the
	// panel warns that public delivery would expose their files in the bucket.
	PrivatePackCount int               `json:"privatePackCount"`
	Notes            map[string]string `json:"notes"`
}

// classifyReachable maps a SafeHead result to the publicReachable tri-state:
// transport error => unknown (nil); 401/403 => not publicly readable (false);
// anything else (2xx/3xx/404/...) => reachable (true).
func classifyReachable(status int, err error) *bool {
	if err != nil {
		return nil
	}
	b := status != http.StatusUnauthorized && status != http.StatusForbidden
	return &b
}

// buildDeliveryCapabilities is the pure assembler (canPresign from a provider
// probe, mirrorURL from settings, reach from SafeHead) into the panel payload.
func buildDeliveryCapabilities(canPresign bool, mirrorURL string, reach *bool, privatePackCount int) deliveryCapabilities {
	mirrorURL = strings.TrimSpace(mirrorURL)
	publicConfigured := mirrorURL != "" && validatePublicBaseURL("solder mirror URL", mirrorURL) == nil
	notes := map[string]string{}
	if !canPresign {
		notes["presigned"] = "The current storage backend can't produce presigned URLs. Presigned delivery needs S3/R2-backed storage."
	}
	if !publicConfigured {
		notes["public"] = "Set a valid public Solder mirror URL to use public delivery."
	} else if reach != nil && !*reach {
		notes["public"] = "The configured mirror URL is not publicly readable (it returned 401/403)."
	}
	return deliveryCapabilities{
		CanPresign:       canPresign,
		PublicConfigured: publicConfigured,
		PublicReachable:  reach,
		PrivatePackCount: privatePackCount,
		Notes:            notes,
	}
}

// DeliveryCapabilities GET /api/admin/settings/modpacks/delivery-capabilities.
// Reflects the SAVED storage config only — it never presigns or HTTP-probes a
// caller-supplied URL, so it is not an SSRF lever.
func (h *ModpackSettingsHandler) DeliveryCapabilities(w http.ResponseWriter, r *http.Request) {
	get := func(k string) string { v, _ := h.state.Store.GetSetting(k); return v }

	canPresign := false
	if prov, err := h.state.buildModpackStorageProvider(); err == nil && prov != nil {
		if u, e := prov.DownloadURL(r.Context(), "solder/__presign_probe__", time.Minute); e == nil && u != "" {
			canPresign = true
		}
	}

	mirrorURL := strings.TrimSpace(get("solder_mirror_url"))
	var reach *bool
	if mirrorURL != "" && validatePublicBaseURL("solder mirror URL", mirrorURL) == nil {
		status, err := services.SafeHead(r.Context(), mirrorURL, 5*time.Second)
		reach = classifyReachable(status, err)
	}

	privatePacks, _ := h.state.Store.CountPrivateSolderPacks()
	caps := buildDeliveryCapabilities(canPresign, mirrorURL, reach, privatePacks)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "capabilities": caps})
}
