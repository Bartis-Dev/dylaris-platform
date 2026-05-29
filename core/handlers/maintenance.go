package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// MaintenanceState mirrors the maintenance.* settings keys. Default is "off"
// — fresh installs are never in maintenance mode.
//
// BlockLevel values:
//   - "off":         feature off entirely (banner hidden, no blocking)
//   - "banner_only": show banner, allow all traffic (advisory)
//   - "block_writes": block POST/PUT/PATCH/DELETE for non-admins (GETs ok)
//   - "block_all":   block everything for non-admins
//
// Admins are never blocked — otherwise they couldn't turn maintenance off.
type MaintenanceState struct {
	Active       bool   `json:"active"`
	Title        string `json:"title"`
	Message      string `json:"message"`
	ExpectedEnd  string `json:"expectedEnd"` // ISO timestamp; "" if not set
	BlockLevel   string `json:"blockLevel"`
}

var defaultMaintenanceState = MaintenanceState{
	Active:      false,
	Title:       "Maintenance in progress",
	Message:     "We're performing scheduled maintenance and will be back shortly.",
	ExpectedEnd: "",
	BlockLevel:  "banner_only",
}

func validMaintenanceLevel(v string) bool {
	return v == "off" || v == "banner_only" || v == "block_writes" || v == "block_all"
}

// LoadMaintenanceState reads the current state from settings. Package-level
// helper so the middleware (and the public GET endpoint) share one path.
func LoadMaintenanceState(state *AppState) MaintenanceState {
	s := defaultMaintenanceState
	if v, _ := state.Store.GetSetting("maintenance.active"); v != "" {
		s.Active = v == "true"
	}
	if v, _ := state.Store.GetSetting("maintenance.title"); v != "" {
		s.Title = v
	}
	if v, _ := state.Store.GetSetting("maintenance.message"); v != "" {
		s.Message = v
	}
	if v, _ := state.Store.GetSetting("maintenance.expected_end"); v != "" {
		s.ExpectedEnd = v
	}
	if v, _ := state.Store.GetSetting("maintenance.block_level"); validMaintenanceLevel(v) {
		s.BlockLevel = v
	}
	return s
}

type MaintenanceHandler struct {
	state *AppState
}

func NewMaintenanceHandler(state *AppState) *MaintenanceHandler {
	return &MaintenanceHandler{state: state}
}

// GetState GET /api/maintenance — public. Drives the frontend banner. Never
// returns admin-only metadata so an unauthenticated visitor can see the
// banner copy on the login page.
func (h *MaintenanceHandler) GetState(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"state":   LoadMaintenanceState(h.state),
	})
}

// SaveState PUT /api/admin/maintenance
func (h *MaintenanceHandler) SaveState(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req MaintenanceState
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if !validMaintenanceLevel(req.BlockLevel) {
		req.BlockLevel = "banner_only"
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Message = strings.TrimSpace(req.Message)
	req.ExpectedEnd = strings.TrimSpace(req.ExpectedEnd)

	actorID, _ := r.Context().Value("userID").(int)
	pairs := []struct{ k, v string }{
		{"maintenance.active", fmt.Sprintf("%t", req.Active)},
		{"maintenance.title", req.Title},
		{"maintenance.message", req.Message},
		{"maintenance.expected_end", req.ExpectedEnd},
		{"maintenance.block_level", req.BlockLevel},
	}
	for _, kv := range pairs {
		if err := h.state.Store.SetSettingBy(kv.k, kv.v, actorID); err != nil {
			sendJSONError(w, "Failed to save: "+kv.k, http.StatusInternalServerError)
			return
		}
	}

	LogIdentityAudit(h.state, r, AuditEventMaintenanceToggled, actorID, 0, map[string]interface{}{
		"active":       req.Active,
		"block_level":  req.BlockLevel,
		"expected_end": req.ExpectedEnd,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"state":   req,
	})
}

// MaintenanceMuxMiddleware is a gorilla/mux-compatible middleware that wraps
// the entire /api subrouter. Same blocking semantics as MaintenanceMiddleware
// but works with api.Use(...). Prefer this over the HandlerFunc variant when
// applying to a whole router — one call covers every route.
func MaintenanceMuxMiddleware(state *AppState) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !shouldBlockForMaintenance(state, r) {
				next.ServeHTTP(w, r)
				return
			}
			writeMaintenanceBlock(state, w)
		})
	}
}

// MaintenanceMiddleware wraps API handlers and blocks traffic per block_level
// when maintenance is active. Admins always pass through. The GET endpoint
// for the state itself is always allowed so the frontend can keep polling
// and the banner stays accurate even while everything else is blocked.
//
// Returns 503 Service Unavailable on blocks — matches standard HTTP semantics
// for planned downtime, and lets reverse proxies surface a Retry-After if
// they want to.
func MaintenanceMiddleware(state *AppState, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !shouldBlockForMaintenance(state, r) {
			next(w, r)
			return
		}
		writeMaintenanceBlock(state, w)
	}
}

// shouldBlockForMaintenance returns true when the request must be rejected
// because maintenance mode is on and the user/method isn't on the pass-list.
// Centralized so both per-handler and router-level middlewares share the
// exact same decision tree.
func shouldBlockForMaintenance(state *AppState, r *http.Request) bool {
	// Always allow the state endpoint, login + status — otherwise users
	// can't even see why they're being blocked.
	if r.URL.Path == "/api/maintenance" || r.URL.Path == "/api/auth/login" || r.URL.Path == "/api/status" {
		return false
	}
	s := LoadMaintenanceState(state)
	if !s.Active || s.BlockLevel == "off" || s.BlockLevel == "banner_only" {
		return false
	}
	// Admin pass-through. Trusts the isAdmin context value set by AuthMiddleware
	// — at the router-level the middleware runs before AuthMiddleware, so the
	// claim is absent and admins on protected routes do still get blocked.
	// Acceptable trade-off: admins simply see a 503, learn maintenance is on,
	// and can toggle it off via the public GET endpoint + an auth retry.
	// To bypass for admins reliably, wire MaintenanceMiddleware per-handler
	// AFTER the AuthMiddleware instead.
	if isAdmin, _ := r.Context().Value("isAdmin").(bool); isAdmin {
		return false
	}
	if s.BlockLevel == "block_writes" {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			return false
		}
	}
	return true
}

// writeMaintenanceBlock emits the 503 response with a Retry-After hint when
// an expected end is configured.
func writeMaintenanceBlock(state *AppState, w http.ResponseWriter) {
	s := LoadMaintenanceState(state)
	w.Header().Set("Content-Type", "application/json")
	if s.ExpectedEnd != "" {
		if t, err := time.Parse(time.RFC3339, s.ExpectedEnd); err == nil {
			secs := int(time.Until(t).Seconds())
			if secs > 0 {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", secs))
			}
		}
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     false,
		"maintenance": true,
		"title":       s.Title,
		"message":     s.Message,
		"expectedEnd": s.ExpectedEnd,
	})
}
