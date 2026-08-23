package handlers

import (
	"encoding/json"
	"net/http"
)

type SystemHandler struct {
	// region + coreID are stamped at construction so the core-info endpoint
	// doesn't have to thread cfg through every request.
	region string
	coreID string
	// tabProxyOrigin + tabProxyIsolationActive mirror config (spec B5): the
	// browser-facing origin-isolated tab-proxy origin and whether isolation is
	// active. Surfaced on the PUBLIC core-info endpoint so BOTH the authed panel
	// and the anonymous /c/<token> share page can learn which origin to build
	// proxied-iframe srcs against.
	tabProxyOrigin          string
	tabProxyIsolationActive bool
}

// NewSystemHandler builds the handler. region + coreID come from config
// (DYLARIS_REGION, DYLARIS_CORE_ID). region defaults to "default" upstream.
// tabProxyOrigin + tabProxyIsolationActive come from config (spec B5).
func NewSystemHandler(region, coreID, tabProxyOrigin string, tabProxyIsolationActive bool) *SystemHandler {
	if region == "" {
		region = "default"
	}
	return &SystemHandler{
		region:                  region,
		coreID:                  coreID,
		tabProxyOrigin:          tabProxyOrigin,
		tabProxyIsolationActive: tabProxyIsolationActive,
	}
}

// GetCapabilities returns the system capabilities
func (h *SystemHandler) GetCapabilities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"mode":            "business",
		"max_nodes":       9999,
		"max_gates":       9999,
		"can_whitelabel":  true,
		"can_custom_jars": true,
		"can_use_dns":     true,
	})
}

// GetCoreInfo returns the region + id of the Core instance that handled the
// request. Drives the topbar "Connected to <region> Core" chip — handy for
// admins triaging "which Core am I hitting" when DNS-georouting splits load.
// Public on the auth-required surface; no secrets in here.
func (h *SystemHandler) GetCoreInfo(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":                 true,
		"region":                  h.region,
		"coreId":                  h.coreID,
		"tabProxyOrigin":          h.tabProxyOrigin,
		"tabProxyIsolationActive": h.tabProxyIsolationActive,
	})
}
