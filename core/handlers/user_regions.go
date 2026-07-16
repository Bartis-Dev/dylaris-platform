package handlers

import (
	"encoding/json"
	"net/http"
)

type UserRegionsHandler struct {
	state *AppState
}

func NewUserRegionsHandler(state *AppState) *UserRegionsHandler {
	return &UserRegionsHandler{state: state}
}

type userRegionsResponse struct {
	AllRegions bool     `json:"allRegions"`
	Regions    []string `json:"regions"`
}

// GetUserRegions GET /api/admin/users/{id}/regions — RequireCap("regions.read") at the route.
func (h *UserRegionsHandler) GetUserRegions(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUserID(w, r)
	if !ok {
		return
	}
	u, err := h.state.Store.GetUserByID(userID)
	if err != nil {
		sendJSONError(w, "User not found", http.StatusNotFound)
		return
	}

	regions, _ := h.state.Store.GetUserRegionIDs(userID)
	if regions == nil {
		regions = []string{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"userId":     u.ID,
		"allRegions": u.AllRegionsAccess,
		"regions":    regions,
	})
}

type setUserRegionsRequest struct {
	AllRegions bool     `json:"allRegions"`
	Regions    []string `json:"regions"`
}

// SetUserRegions PUT /api/admin/users/{id}/regions — RequireCap("regions.write") at the route.
// Replaces the user's region assignment in one transaction. When allRegions
// is true the explicit list is wiped and the user gets blanket access.
func (h *UserRegionsHandler) SetUserRegions(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUserID(w, r)
	if !ok {
		return
	}
	if _, err := h.state.Store.GetUserByID(userID); err != nil {
		sendJSONError(w, "User not found", http.StatusNotFound)
		return
	}

	var req setUserRegionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate each explicit region id exists + is enabled (only when not all-access)
	if !req.AllRegions {
		for _, rid := range req.Regions {
			region, err := h.state.Store.GetRegion(rid)
			if err != nil || region == nil {
				sendJSONError(w, "Unknown region: "+rid, http.StatusBadRequest)
				return
			}
		}
	}

	if err := h.state.Store.SetUserRegions(userID, req.AllRegions, req.Regions); err != nil {
		sendJSONError(w, "Failed to update user regions", http.StatusInternalServerError)
		return
	}

	actorID, _ := r.Context().Value("userID").(string)
	LogIdentityAudit(h.state, r, AuditEventUserRegionsChanged, actorID, userID, map[string]interface{}{
		"all_regions": req.AllRegions,
		"regions":     req.Regions,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"allRegions": req.AllRegions,
		"regions":    req.Regions,
	})
}

// GetMyRegions GET /api/me/regions — current user's own region assignment
// (any authenticated user). Drives the frontend's "should I show a region
// selector or hide it for single-region UX?" decision.
func (h *UserRegionsHandler) GetMyRegions(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(string)
	if !ok {
		sendJSONError(w, "Unauthenticated", http.StatusUnauthorized)
		return
	}
	u, err := h.state.Store.GetUserByID(userID)
	if err != nil {
		sendJSONError(w, "User not found", http.StatusNotFound)
		return
	}
	regions, _ := h.state.Store.GetUserRegionIDs(userID)
	if regions == nil {
		regions = []string{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"allRegions": u.AllRegionsAccess,
		"regions":    regions,
	})
}
