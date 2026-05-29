package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// AuditPolicy holds the platform-wide audit settings. Today only the
// server-audit retention horizon; in future this gains identity-audit
// retention and per-event-type retention overrides.
type AuditPolicy struct {
	ServerRetentionDays int `json:"serverRetentionDays"`
}

var defaultAuditPolicy = AuditPolicy{
	ServerRetentionDays: 365,
}

func LoadAuditPolicy(state *AppState) AuditPolicy {
	p := defaultAuditPolicy
	if v, _ := state.Store.GetSetting("audit.server_retention_days"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 && n <= 3650 {
			p.ServerRetentionDays = n
		}
	}
	return p
}

type AuditSettingsHandler struct {
	state *AppState
}

func NewAuditSettingsHandler(state *AppState) *AuditSettingsHandler {
	return &AuditSettingsHandler{state: state}
}

// GetPolicy GET /api/admin/settings/audit
func (h *AuditSettingsHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"policy":  LoadAuditPolicy(h.state),
	})
}

// SavePolicy PUT /api/admin/settings/audit
func (h *AuditSettingsHandler) SavePolicy(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var p AuditPolicy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if p.ServerRetentionDays < 0 {
		p.ServerRetentionDays = 0
	}
	if p.ServerRetentionDays > 3650 {
		p.ServerRetentionDays = 3650
	}
	actorID, _ := r.Context().Value("userID").(int)
	if err := h.state.Store.SetSettingBy("audit.server_retention_days", fmt.Sprintf("%d", p.ServerRetentionDays), actorID); err != nil {
		sendJSONError(w, "Failed to save", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"policy":  p,
	})
}
