package handlers

import (
	"dylaris-core/store"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

// PlansHandler exposes admin CRUD for BYON plan tiers + assigning a plan / limit
// overrides to a tenant. All admin-only. Limits are interpreted as 0 = unlimited.
type PlansHandler struct {
	state *AppState
}

func NewPlansHandler(state *AppState) *PlansHandler { return &PlansHandler{state: state} }

type planBody struct {
	Name              string `json:"name"`
	PriceLabel        string `json:"priceLabel"`
	MaxNodes          int64  `json:"maxNodes"`
	MaxLinks          int64  `json:"maxLinks"`
	R2QuotaGb         int64  `json:"r2QuotaGb"`
	TrafficEdgeGb     int64  `json:"trafficEdgeGb"`
	TrafficRelayGb    int64  `json:"trafficRelayGb"`
	TrafficCombinedGb int64  `json:"trafficCombinedGb"`
	IsDefault         bool   `json:"isDefault"`
}

func (b planBody) toPlan(id int) store.Plan {
	return store.Plan{
		ID: id, Name: b.Name, PriceLabel: b.PriceLabel, MaxNodes: b.MaxNodes, MaxLinks: b.MaxLinks,
		R2QuotaGB: b.R2QuotaGb, TrafficEdgeGB: b.TrafficEdgeGb, TrafficRelayGB: b.TrafficRelayGb,
		TrafficCombinedGB: b.TrafficCombinedGb, IsDefault: b.IsDefault,
	}
}

func (b planBody) valid() bool {
	return b.Name != "" && b.MaxNodes >= 0 && b.MaxLinks >= 0 && b.R2QuotaGb >= 0 &&
		b.TrafficEdgeGb >= 0 && b.TrafficRelayGb >= 0 && b.TrafficCombinedGb >= 0
}

// List GET /api/admin/plans - RequireCap("plans.read")
func (h *PlansHandler) List(w http.ResponseWriter, r *http.Request) {
	plans, err := h.state.Store.ListPlans()
	if err != nil {
		sendJSONError(w, "Failed to list plans", http.StatusInternalServerError)
		return
	}
	if plans == nil {
		plans = []store.Plan{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "plans": plans})
}

// Create POST /api/admin/plans - RequireCap("plans.write")
func (h *PlansHandler) Create(w http.ResponseWriter, r *http.Request) {
	var b planBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || !b.valid() {
		sendJSONError(w, "name is required and limits must be >= 0 (0 = unlimited)", http.StatusBadRequest)
		return
	}
	id, err := h.state.Store.CreatePlan(b.toPlan(0))
	if err != nil {
		sendJSONError(w, "Failed to create plan", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "id": id})
}

// Update PUT /api/admin/plans/{id} - RequireCap("plans.write")
func (h *PlansHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	if id <= 0 {
		sendJSONError(w, "Invalid plan id", http.StatusBadRequest)
		return
	}
	var b planBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || !b.valid() {
		sendJSONError(w, "name is required and limits must be >= 0 (0 = unlimited)", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.UpdatePlan(b.toPlan(id)); err != nil {
		sendJSONError(w, "Failed to update plan", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// Delete DELETE /api/admin/plans/{id} - RequireCap("plans.delete")
func (h *PlansHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	if id <= 0 {
		sendJSONError(w, "Invalid plan id", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.DeletePlan(id); err != nil {
		sendJSONError(w, "Failed to delete plan", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// SetUserPlan PATCH /api/admin/users/{id}/plan - RequireCap("plans.write").
// Body {planId: int|null}; null clears the assignment (the tenant falls back
// to the default plan).
func (h *PlansHandler) SetUserPlan(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["id"]
	var req struct {
		PlanID *int `json:"planId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.SetUserPlan(userID, req.PlanID); err != nil {
		sendJSONError(w, "Failed to set plan", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// SetUserLimitOverrides PATCH /api/admin/users/{id}/limit-overrides -
// RequireCap("plans.write"). Per-user limit overrides (nil clears one -> use
// the plan value). Does not touch the R2 quota override (that lives on
// /billing-overrides).
func (h *PlansHandler) SetUserLimitOverrides(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["id"]
	var req struct {
		MaxNodes          *int64 `json:"maxNodes"`
		MaxLinks          *int64 `json:"maxLinks"`
		TrafficEdgeGb     *int64 `json:"trafficEdgeGb"`
		TrafficRelayGb    *int64 `json:"trafficRelayGb"`
		TrafficCombinedGb *int64 `json:"trafficCombinedGb"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	for _, v := range []*int64{req.MaxNodes, req.MaxLinks, req.TrafficEdgeGb, req.TrafficRelayGb, req.TrafficCombinedGb} {
		if v != nil && *v < 0 {
			sendJSONError(w, "Limit overrides must be >= 0 (0 = unlimited)", http.StatusBadRequest)
			return
		}
	}
	if err := h.state.Store.SetUserLimitOverrides(userID, req.MaxNodes, req.MaxLinks, req.TrafficEdgeGb, req.TrafficRelayGb, req.TrafficCombinedGb); err != nil {
		sendJSONError(w, "Failed to save overrides", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
