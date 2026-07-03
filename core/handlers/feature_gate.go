package handlers

import (
	"encoding/json"
	"net/http"
)

// FeatureModpacks is the canonical name used in X-Feature-Disabled headers
// + JSON error responses for the modpacks feature.
const FeatureModpacks = "modpacks"

// FeatureTickets is the canonical name used in X-Feature-Disabled headers
// + JSON error responses for the ticket subsystem.
const FeatureTickets = "tickets"

// FeatureShareLinks is the canonical name for the modpack share-links sub-feature.
const FeatureShareLinks = "modpack_share_links"

// RequireModpacksEnabled blocks the request with 503 feature_disabled when
// the platform-wide modpacks toggle is off. Use on every WRITE endpoint that
// touches modpack data (modpacks CRUD, versions, mods, publish, mrpack PAT
// management).
func (s *AppState) RequireModpacksEnabled(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.FeatureFlags.IsModpacksEnabled(r.Context()) {
			featureDisabledResponse(w, FeatureModpacks, "Modpack authoring is disabled by the platform admin.")
			return
		}
		next(w, r)
	}
}

// RequireShareLinksEnabled blocks share-link CREATION with 503 feature_disabled
// when the admin has not enabled share links. Layer it between
// RequireModpacksEnabled and RequireUserCanCreateModpacks.
func (s *AppState) RequireShareLinksEnabled(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.FeatureFlags.IsShareLinksEnabled(r.Context()) {
			featureDisabledResponse(w, FeatureShareLinks, "Share links are disabled by the platform admin.")
			return
		}
		next(w, r)
	}
}

// RequireTicketsEnabled blocks the request with 503 feature_disabled when
// the platform-wide tickets toggle is off. Wrap every endpoint under the
// ticket subsystem (tickets, categories, attachments, canned responses,
// notifications, settings) so the panel + external clients all see the same
// 503 when the admin pauses tickets.
func (s *AppState) RequireTicketsEnabled(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.FeatureFlags.IsTicketsEnabled(r.Context()) {
			featureDisabledResponse(w, FeatureTickets, "The ticket system is disabled by the platform admin.")
			return
		}
		next(w, r)
	}
}

// AllowReadOnlyWhenDisabled lets the request through but sets the
// X-Feature-Disabled response header when the toggle is off. Used on READ
// endpoints (list/get modpacks, download .mrpack) so existing modpacks stay
// viewable + downloadable when the feature is paused.
func (s *AppState) AllowReadOnlyWhenDisabled(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.FeatureFlags.IsModpacksEnabled(r.Context()) {
			w.Header().Set("X-Feature-Disabled", FeatureModpacks)
		}
		next(w, r)
	}
}

// RequireUserCanCreateModpacks checks the per-user can_create_modpacks flag.
// Admins bypass. Layer this AFTER RequireModpacksEnabled so the user lookup
// only happens when the platform check passes.
func (s *AppState) RequireUserCanCreateModpacks(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isAdmin, _ := r.Context().Value("isAdmin").(bool); isAdmin {
			next(w, r)
			return
		}
		userID, _ := r.Context().Value("userID").(string)
		if userID == "" {
			sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		user, err := s.Store.GetUserByID(userID)
		if err != nil || user == nil {
			sendJSONError(w, "User not found", http.StatusUnauthorized)
			return
		}
		if !user.CanCreateModpacks {
			featureDisabledResponse(w, "modpacks_user", "Your account is not permitted to author modpacks.")
			return
		}
		next(w, r)
	}
}

// featureDisabledResponse writes the canonical 503 + X-Feature-Disabled +
// machine-readable error body.
func featureDisabledResponse(w http.ResponseWriter, feature, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Feature-Disabled", feature)
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   "feature_disabled",
		"feature": feature,
		"message": msg,
	})
}
