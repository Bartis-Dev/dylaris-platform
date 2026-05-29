package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// TicketSettings mirrors the tickets.* keys. Defaults are chosen so a
// freshly-enabled Ticket module is friendly:
//   - cross_team_visibility ON  → no team setup needed before the first ticket
//   - watchers_default_can_reply OFF → CC starts read-only by convention
//   - allow_users_to_add_watchers ON → users self-serve CC additions
//   - audit_retention_days 180 → six months, generous enough for most ops
type TicketSettings struct {
	CrossTeamVisibility      bool `json:"crossTeamVisibility"`
	WatchersDefaultCanReply  bool `json:"watchersDefaultCanReply"`
	AllowUsersToAddWatchers  bool `json:"allowUsersToAddWatchers"`
	AuditRetentionDays       int  `json:"auditRetentionDays"`

	// Phase 3 — attachment quotas (megabytes). 0 = unlimited.
	MaxFileSizeMB   int `json:"maxFileSizeMb"`
	MaxTicketSizeMB int `json:"maxTicketSizeMb"`
	MaxUserSizeMB   int `json:"maxUserSizeMb"`

	// Phase 3 — auto-close. When enabled, resolved tickets get closed by
	// the background job after AutoCloseDaysAfterResolved days of no activity.
	AutoCloseEnabled           bool `json:"autoCloseEnabled"`
	AutoCloseDaysAfterResolved int  `json:"autoCloseDaysAfterResolved"`
}

var defaultTicketSettings = TicketSettings{
	CrossTeamVisibility:        true,
	WatchersDefaultCanReply:    false,
	AllowUsersToAddWatchers:    true,
	AuditRetentionDays:         180,
	MaxFileSizeMB:              10,
	MaxTicketSizeMB:            50,
	MaxUserSizeMB:              500,
	AutoCloseEnabled:           false,
	AutoCloseDaysAfterResolved: 7,
}

func LoadTicketSettings(state *AppState) TicketSettings {
	s := defaultTicketSettings
	if v, _ := state.Store.GetSetting("tickets.cross_team_visibility"); v != "" {
		s.CrossTeamVisibility = v == "true"
	}
	if v, _ := state.Store.GetSetting("tickets.watchers_default_can_reply"); v != "" {
		s.WatchersDefaultCanReply = v == "true"
	}
	if v, _ := state.Store.GetSetting("tickets.allow_users_to_add_watchers"); v != "" {
		s.AllowUsersToAddWatchers = v == "true"
	}
	if v, _ := state.Store.GetSetting("tickets.audit_retention_days"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 && n <= 3650 {
			s.AuditRetentionDays = n
		}
	}
	if v, _ := state.Store.GetSetting("tickets.max_file_size_mb"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 && n <= 1024 {
			s.MaxFileSizeMB = n
		}
	}
	if v, _ := state.Store.GetSetting("tickets.max_ticket_size_mb"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 && n <= 10240 {
			s.MaxTicketSizeMB = n
		}
	}
	if v, _ := state.Store.GetSetting("tickets.max_user_size_mb"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 && n <= 102400 {
			s.MaxUserSizeMB = n
		}
	}
	if v, _ := state.Store.GetSetting("tickets.auto_close_enabled"); v != "" {
		s.AutoCloseEnabled = v == "true"
	}
	if v, _ := state.Store.GetSetting("tickets.auto_close_days_after_resolved"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 1 && n <= 365 {
			s.AutoCloseDaysAfterResolved = n
		}
	}
	return s
}

type TicketSettingsHandler struct {
	state *AppState
}

func NewTicketSettingsHandler(state *AppState) *TicketSettingsHandler {
	return &TicketSettingsHandler{state: state}
}

// GetSettings GET /api/admin/settings/tickets
func (h *TicketSettingsHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": LoadTicketSettings(h.state),
	})
}

// SaveSettings PUT /api/admin/settings/tickets
func (h *TicketSettingsHandler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var s TicketSettings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if s.AuditRetentionDays < 0 {
		s.AuditRetentionDays = 0
	}
	if s.AuditRetentionDays > 3650 {
		s.AuditRetentionDays = 3650
	}
	if s.MaxFileSizeMB < 0 {
		s.MaxFileSizeMB = 0
	}
	if s.MaxTicketSizeMB < 0 {
		s.MaxTicketSizeMB = 0
	}
	if s.MaxUserSizeMB < 0 {
		s.MaxUserSizeMB = 0
	}
	if s.AutoCloseDaysAfterResolved < 1 {
		s.AutoCloseDaysAfterResolved = 1
	}
	if s.AutoCloseDaysAfterResolved > 365 {
		s.AutoCloseDaysAfterResolved = 365
	}
	actorID, _ := r.Context().Value("userID").(int)
	pairs := []struct{ k, v string }{
		{"tickets.cross_team_visibility", fmt.Sprintf("%t", s.CrossTeamVisibility)},
		{"tickets.watchers_default_can_reply", fmt.Sprintf("%t", s.WatchersDefaultCanReply)},
		{"tickets.allow_users_to_add_watchers", fmt.Sprintf("%t", s.AllowUsersToAddWatchers)},
		{"tickets.audit_retention_days", fmt.Sprintf("%d", s.AuditRetentionDays)},
		{"tickets.max_file_size_mb", fmt.Sprintf("%d", s.MaxFileSizeMB)},
		{"tickets.max_ticket_size_mb", fmt.Sprintf("%d", s.MaxTicketSizeMB)},
		{"tickets.max_user_size_mb", fmt.Sprintf("%d", s.MaxUserSizeMB)},
		{"tickets.auto_close_enabled", fmt.Sprintf("%t", s.AutoCloseEnabled)},
		{"tickets.auto_close_days_after_resolved", fmt.Sprintf("%d", s.AutoCloseDaysAfterResolved)},
	}
	for _, kv := range pairs {
		if err := h.state.Store.SetSettingBy(kv.k, kv.v, actorID); err != nil {
			sendJSONError(w, "Failed to save: "+kv.k, http.StatusInternalServerError)
			return
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": s,
	})
}
