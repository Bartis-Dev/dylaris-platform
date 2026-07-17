package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Beam update-channel values (users.beam_update_channel).
const (
	beamChannelStable = "stable"
	beamChannelDev    = "dev"
)

// beam.dev_channel_access policy values. Gates who may opt into the dev
// (prerelease) update channel.
const (
	beamDevAccessDisabled   = "disabled"
	beamDevAccessAdminsOnly = "admins-only"
	beamDevAccessAllUsers   = "all-users"
)

// validBeamChannel reports whether ch is a known channel.
func validBeamChannel(ch string) bool {
	return ch == beamChannelStable || ch == beamChannelDev
}

// beamDevChannelAllowed reports whether a user may opt into the dev channel under
// the given policy. Unknown/empty policy fails safe to false (disabled).
func beamDevChannelAllowed(policy string, isAdmin bool) bool {
	switch strings.TrimSpace(policy) {
	case beamDevAccessAllUsers:
		return true
	case beamDevAccessAdminsOnly:
		return isAdmin
	default: // disabled or unknown
		return false
	}
}

// resolveBeamChannel returns the EFFECTIVE channel for a user: 'dev' only when
// the user picked it AND the policy admits them; otherwise 'stable'. So revoking
// the policy silently drops everyone back to stable without rewriting their
// stored preference (which is restored if the policy is re-enabled).
func resolveBeamChannel(pref, policy string, isAdmin bool) string {
	if strings.TrimSpace(pref) == beamChannelDev && beamDevChannelAllowed(policy, isAdmin) {
		return beamChannelDev
	}
	return beamChannelStable
}

// GetMyBeamChannel GET /api/me/beam-channel - the caller's stored channel pref,
// the effective channel (pref clamped by policy), and whether dev is available
// to them right now. Own data, authed-exempt (no capability).
func (h *BeamHandler) GetMyBeamChannel(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	isAdmin, _ := r.Context().Value("isAdmin").(bool)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	pref, err := h.state.Store.GetUserBeamChannel(userID)
	if err != nil {
		sendJSONError(w, "User not found", http.StatusNotFound)
		return
	}
	policy, _ := h.state.Store.GetSetting("beam.dev_channel_access")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"channel":           pref,
		"effective_channel": resolveBeamChannel(pref, policy, isAdmin),
		"dev_allowed":       beamDevChannelAllowed(policy, isAdmin),
	})
}

// SetMyBeamChannel PUT /api/me/beam-channel {channel} - set the caller's own
// channel. Validates the value and enforces the dev-access policy before storing
// 'dev', so a user cannot self-elevate onto the dev channel by calling the API
// directly when the policy forbids it.
func (h *BeamHandler) SetMyBeamChannel(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	isAdmin, _ := r.Context().Value("isAdmin").(bool)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Channel string `json:"channel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	channel := strings.ToLower(strings.TrimSpace(req.Channel))
	if !validBeamChannel(channel) {
		sendJSONError(w, "Invalid channel: must be 'stable' or 'dev'", http.StatusBadRequest)
		return
	}
	if channel == beamChannelDev {
		policy, _ := h.state.Store.GetSetting("beam.dev_channel_access")
		if !beamDevChannelAllowed(policy, isAdmin) {
			sendJSONError(w, "The dev channel is not available for your account", http.StatusForbidden)
			return
		}
	}
	if err := h.state.Store.SetUserBeamChannel(userID, channel); err != nil {
		sendJSONError(w, "Failed to save channel", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"channel": channel,
	})
}
