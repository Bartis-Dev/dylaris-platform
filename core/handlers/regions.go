package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"dylaris-core/models"

	"github.com/gorilla/mux"
)

// regionIDRegex enforces a safe ID syntax — short, lowercase, hyphen-friendly.
// IDs are referenced by nodes.region / servers.region and must round-trip
// through URLs without escaping.
var regionIDRegex = regexp.MustCompile(`^[a-z][a-z0-9\-]{0,30}[a-z0-9]$`)

type RegionsHandler struct {
	state *AppState
}

func NewRegionsHandler(state *AppState) *RegionsHandler {
	return &RegionsHandler{state: state}
}

// ListRegions GET /api/regions — available to all authenticated users.
// Returns only enabled regions; the admin variant returns all.
func (h *RegionsHandler) ListRegions(w http.ResponseWriter, r *http.Request) {
	regions, err := h.state.Store.ListRegions(false)
	if err != nil {
		sendJSONError(w, "Failed to load regions", http.StatusInternalServerError)
		return
	}
	if regions == nil {
		regions = []models.Region{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"regions": regions,
	})
}

// AdminListRegions GET /api/admin/regions — admin-only; includes disabled regions.
func (h *RegionsHandler) AdminListRegions(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	regions, err := h.state.Store.ListRegions(true)
	if err != nil {
		sendJSONError(w, "Failed to load regions", http.StatusInternalServerError)
		return
	}
	if regions == nil {
		regions = []models.Region{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"regions": regions,
	})
}

type regionRequest struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Enabled     *bool  `json:"enabled,omitempty"`
	Color       string `json:"color"`
}

// CreateRegion POST /api/admin/regions
func (h *RegionsHandler) CreateRegion(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req regionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	id := strings.ToLower(strings.TrimSpace(req.ID))
	displayName := strings.TrimSpace(req.DisplayName)
	if !regionIDRegex.MatchString(id) {
		sendJSONError(w, "Invalid region id (use lowercase letters, digits and hyphens; 2-32 chars)", http.StatusBadRequest)
		return
	}
	if displayName == "" {
		sendJSONError(w, "Display name is required", http.StatusBadRequest)
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	region := &models.Region{
		ID:          id,
		DisplayName: displayName,
		Enabled:     enabled,
		Color:       strings.TrimSpace(req.Color),
	}
	if err := h.state.Store.CreateRegion(region); err != nil {
		sendJSONError(w, "Failed to create region (id may already exist)", http.StatusConflict)
		return
	}

	actorID, _ := r.Context().Value("userID").(int)
	LogIdentityAudit(h.state, r, AuditEventRegionCreated, actorID, 0, map[string]interface{}{
		"region_id":   region.ID,
		"display_name": region.DisplayName,
	})

	// Re-read so created_at is populated.
	created, _ := h.state.Store.GetRegion(region.ID)
	if created == nil {
		created = region
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"region":  created,
	})
}

// UpdateRegion PATCH /api/admin/regions/{id}
func (h *RegionsHandler) UpdateRegion(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	id := mux.Vars(r)["id"]
	existing, err := h.state.Store.GetRegion(id)
	if err != nil {
		sendJSONError(w, "Region not found", http.StatusNotFound)
		return
	}

	var req regionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.DisplayName) != "" {
		existing.DisplayName = strings.TrimSpace(req.DisplayName)
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	// Allow clearing the color by sending an empty string explicitly.
	existing.Color = strings.TrimSpace(req.Color)

	if err := h.state.Store.UpdateRegion(existing); err != nil {
		sendJSONError(w, "Failed to update region", http.StatusInternalServerError)
		return
	}

	actorID, _ := r.Context().Value("userID").(int)
	LogIdentityAudit(h.state, r, AuditEventRegionUpdated, actorID, 0, map[string]interface{}{
		"region_id": existing.ID,
		"enabled":   existing.Enabled,
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"region":  existing,
	})
}

// DeleteRegion DELETE /api/admin/regions/{id}
// Refuses if servers or nodes still reference the region — operator must
// reassign or delete those first.
func (h *RegionsHandler) DeleteRegion(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	id := mux.Vars(r)["id"]
	if id == "default" {
		sendJSONError(w, "Cannot delete the default region", http.StatusBadRequest)
		return
	}

	if n, _ := h.state.Store.CountServersInRegion(id); n > 0 {
		sendJSONError(w, "Region still has servers; reassign or delete them first", http.StatusConflict)
		return
	}
	if n, _ := h.state.Store.CountNodesInRegion(id); n > 0 {
		sendJSONError(w, "Region still has nodes; reassign or delete them first", http.StatusConflict)
		return
	}

	if err := h.state.Store.DeleteRegion(id); err != nil {
		sendJSONError(w, "Failed to delete region", http.StatusInternalServerError)
		return
	}

	actorID, _ := r.Context().Value("userID").(int)
	LogIdentityAudit(h.state, r, AuditEventRegionDeleted, actorID, 0, map[string]interface{}{
		"region_id": id,
	})

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
