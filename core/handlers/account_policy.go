package handlers

import (
	"encoding/json"
	"net/http"
)

type AccountPolicyHandler struct {
	state *AppState
}

func NewAccountPolicyHandler(state *AppState) *AccountPolicyHandler {
	return &AccountPolicyHandler{state: state}
}

type accountPolicy struct {
	AllowNameChange        bool `json:"allowNameChange"`
	NameChangeCooldownDays int  `json:"nameChangeCooldownDays"`
}

// Get GET /api/admin/settings/users
func (h *AccountPolicyHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	allow, days, _ := h.state.Store.GetUserAccountPolicy()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"policy":  accountPolicy{AllowNameChange: allow, NameChangeCooldownDays: days},
	})
}

// Set PUT /api/admin/settings/users
func (h *AccountPolicyHandler) Set(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req accountPolicy
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.NameChangeCooldownDays < 0 || req.NameChangeCooldownDays > 3650 {
		sendJSONError(w, "Cooldown must be 0-3650 days", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.SetUserAccountPolicy(req.AllowNameChange, req.NameChangeCooldownDays); err != nil {
		sendJSONError(w, "Save failed", http.StatusInternalServerError)
		return
	}
	h.state.Events.Publish(r.Context(), "users.policy.changed", nil)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
