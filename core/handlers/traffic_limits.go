package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"dylaris-core/services"
)

// TrafficLimitHandler exposes the per-(region, kind) traffic limits.
//
// The limits live in Core rather than in the store because the per-user
// override does: users are Core's, and the three-scope walk
// (user:<id> -> user_default -> global) already exists here for route limits.
// Stripe keeps the price IDs; Core keeps who may use how much.
type TrafficLimitHandler struct {
	state *AppState
}

func NewTrafficLimitHandler(state *AppState) *TrafficLimitHandler {
	return &TrafficLimitHandler{state: state}
}

// trafficLimitBody is the wire shape for a write.
//
// Mode, not a bare number, for the same reason the route limit uses one: the
// three states an operator means are "the scope above decides" (default),
// "decided here, no limit" (unlimited) and "decided here, this many"
// (custom, including zero). A bare nullable number cannot tell the first two
// apart, and that ambiguity is what made a stored 0 mean two different things
// depending on which row it sat in.
type trafficLimitBody struct {
	Scope  string `json:"scope"`  // user:<id> | user_default | global
	Region string `json:"region"` // e.g. eu-central
	Kind   string `json:"kind"`   // edge | relay | warp

	IncludedMode string `json:"includedMode"` // default | unlimited | custom
	IncludedGB   int64  `json:"includedGb"`

	PurchaseMode string `json:"purchaseMode"` // default | unlimited | custom
	PurchaseGB   int64  `json:"purchaseGb"`
}

// ListTrafficLimits GET /api/traffic-limits - every configured row.
//
// Returns rows only. A region with NO row is not listed and that is the honest
// answer: nothing has been decided about it, so nothing limits it. The panel
// has to show that gap against the regions the edges actually report, because
// "no row" and "no limit" look identical from here and only one of them is
// something an operator meant.
func (h *TrafficLimitHandler) ListTrafficLimits(w http.ResponseWriter, r *http.Request) {
	rows, err := h.state.Store.ListTrafficLimits()
	if err != nil {
		http.Error(w, "Failed to read traffic limits", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = nil // keep the empty array shape below rather than null
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"limits":  rows,
	})
}

// ResolveTrafficLimit GET /api/traffic-limits/resolve?user=&region=&kind=
// answers what actually applies, and says which scope decided it. The panel
// shows the scope so an operator can tell an inherited value from an override
// before changing the wrong one.
func (h *TrafficLimitHandler) ResolveTrafficLimit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	region, kind := strings.TrimSpace(q.Get("region")), strings.TrimSpace(q.Get("kind"))
	if region == "" || kind == "" {
		http.Error(w, "region and kind are required", http.StatusBadRequest)
		return
	}
	res, err := services.ResolveTrafficLimit(h.state.Store, strings.TrimSpace(q.Get("user")), region, kind)
	if err != nil {
		http.Error(w, "Failed to resolve traffic limit", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"region":        region,
		"kind":          kind,
		"includedGb":    res.IncludedGB,
		"maxPurchaseGb": res.MaxPurchaseGB,
		"decidedBy":     res.Scope, // "" = nothing decided anywhere
	})
}

// SetTrafficLimit PUT /api/traffic-limits.
//
// "default" for BOTH values deletes the row, because a row that says nothing
// about either question is a row that should not exist - leaving it would make
// the scope answer with two NULLs, which means "no limit", the opposite of
// deferring. That is the one transition where storing and deleting differ, and
// it is the one an operator reaches for when undoing an override.
func (h *TrafficLimitHandler) SetTrafficLimit(w http.ResponseWriter, r *http.Request) {
	var req trafficLimitBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	req.Scope = strings.TrimSpace(req.Scope)
	req.Region = strings.TrimSpace(req.Region)
	req.Kind = strings.TrimSpace(req.Kind)
	if req.Scope == "" || req.Region == "" || req.Kind == "" {
		http.Error(w, "scope, region and kind are required", http.StatusBadRequest)
		return
	}
	if !validTrafficScope(req.Scope) {
		http.Error(w, "scope must be global, user_default or user:<id>", http.StatusBadRequest)
		return
	}

	included, err := limitFromMode(req.IncludedMode, req.IncludedGB)
	if err != nil {
		http.Error(w, "includedMode: "+err.Error(), http.StatusBadRequest)
		return
	}
	purchase, err := limitFromMode(req.PurchaseMode, req.PurchaseGB)
	if err != nil {
		http.Error(w, "purchaseMode: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.IncludedMode == "default" && req.PurchaseMode == "default" {
		if err := h.state.Store.DeleteTrafficLimit(req.Scope, req.Region, req.Kind); err != nil {
			http.Error(w, "Failed to clear traffic limit", http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "cleared": true})
		return
	}

	if err := h.state.Store.SetTrafficLimit(req.Scope, req.Region, req.Kind, included, purchase); err != nil {
		http.Error(w, "Failed to save traffic limit", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// limitFromMode turns the three operator-facing modes into the stored value.
//
// "default" and "unlimited" both produce nil HERE; what separates them is
// whether the row exists at all, which the caller decides. Zero is a legal
// custom value and means none - it must survive as a number, never collapse
// into nil, or the one thing an operator can type to switch a feature off
// becomes the thing that switches the check off.
func limitFromMode(mode string, gb int64) (*int64, error) {
	switch strings.TrimSpace(mode) {
	case "", "default", "unlimited":
		return nil, nil
	case "custom":
		if gb < 0 {
			return nil, errNegativeLimit
		}
		v := gb
		return &v, nil
	default:
		return nil, errBadLimitMode
	}
}

func validTrafficScope(scope string) bool {
	return scope == "global" || scope == "user_default" || strings.HasPrefix(scope, "user:")
}

var (
	errBadLimitMode  = &limitError{"must be default, unlimited or custom"}
	errNegativeLimit = &limitError{"must not be negative"}
)

type limitError struct{ msg string }

func (e *limitError) Error() string { return e.msg }
