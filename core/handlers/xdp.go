package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	sharedxdp "dylaris-pkg/xdp"
)

// XDPHandler exposes admin-only CRUD on the deployment-wide XDP / eBPF DDoS
// protection config. The payload is stored in Redis at sharedxdp.RedisConfigKey
// and consumed by every Edge replica via its 30s reload loop.
type XDPHandler struct {
	state *AppState
}

func NewXDPHandler(state *AppState) *XDPHandler {
	return &XDPHandler{state: state}
}

// GetConfig — GET /api/admin/xdp/config
// Returns the current Redis-backed config or the package defaults when the key
// is empty (so the Panel can pre-fill the form on first open).
func (h *XDPHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	cfg, present, err := sharedxdp.Load(ctx, h.state.Redis)
	if err != nil {
		sendJSONError(w, "Failed to load XDP config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"config":  cfg,
		"present": present, // false = first-time, defaults returned
	})
}

// UpdateConfig — PUT /api/admin/xdp/config
// Validates the payload and writes it to Redis. Edge replicas pick it up
// within ~30s and reconcile their sidecar.
func (h *XDPHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}

	var cfg sharedxdp.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		sendJSONError(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := validateXDPConfig(&cfg); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := sharedxdp.Save(ctx, h.state.Redis, cfg); err != nil {
		sendJSONError(w, "Failed to save XDP config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"config":  cfg,
	})
}

// validateXDPConfig clamps obviously-wrong values and rejects malformed lists.
// We don't fully parse the whitelist here — sharedxdp.ParsedWhitelist already
// silently drops bad entries on the Edge side, so a partial-valid list is fine.
func validateXDPConfig(cfg *sharedxdp.Config) error {
	// Defensive clamps so a bad config doesn't tank XDP performance on the
	// Edge side. Caps chosen to be well above any sensible production setting.
	if cfg.RateLimit == 0 {
		cfg.RateLimit = 1
	}
	if cfg.RateLimit > 1_000_000 {
		cfg.RateLimit = 1_000_000
	}
	if cfg.RateWindowMs == 0 {
		cfg.RateWindowMs = 100
	}
	if cfg.BanDurationMin == 0 {
		cfg.BanDurationMin = 1
	}

	// Ports must be a sane CSV — empty list disables port filtering, which is
	// fine if XDP itself is disabled. If enabled, at least require one port.
	if cfg.Enabled && strings.TrimSpace(cfg.ProtectedPorts) == "" {
		return errBadRequest("protected_ports cannot be empty when enabled")
	}

	return nil
}

type errBadRequestText string

func (e errBadRequestText) Error() string { return string(e) }

func errBadRequest(msg string) error { return errBadRequestText(msg) }
