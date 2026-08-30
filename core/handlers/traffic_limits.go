package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"dylaris-core/models"
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
		// An explicit empty array, not null: the panel maps over this, and a
		// null would have to be defended against at every call site instead of
		// once here.
		rows = []models.TrafficLimit{}
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
	// A region or kind nobody writes is a limit that limits nothing, and it
	// looks identical to a working one on the settings screen. The producers
	// normalise to [a-z0-9-] (meterRegion, on the edge and the relay), so
	// anything else here could never match a counter.
	if !validTrafficLabel(req.Region) {
		http.Error(w, "region must be lowercase letters, digits and dashes", http.StatusBadRequest)
		return
	}
	if !validTrafficLabel(req.Kind) {
		http.Error(w, "kind must be lowercase letters, digits and dashes", http.StatusBadRequest)
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

	// A row is one COMPLETE answer - that is the whole reason both numbers live
	// in one row (see the traffic_limits schema). "default" means this scope
	// says nothing, and a scope cannot say nothing about half a row: storing the
	// deferred side as NULL would make the resolver read it as "decided here, no
	// limit" and stop the walk, so an override meant to cap purchases alone
	// would silently grant unlimited included traffic.
	//
	// Refused rather than guessed: "unlimited" is right there for the operator
	// who does mean no limit, and picking one of the two for them is exactly the
	// ambiguity the mode field exists to remove.
	if (req.IncludedMode == "default") != (req.PurchaseMode == "default") {
		http.Error(w,
			"includedMode and purchaseMode must both be \"default\" or neither: a scope answers a whole row. Use \"unlimited\" for a decided no-limit.",
			http.StatusBadRequest)
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
	if scope == "global" || scope == "user_default" {
		return true
	}
	// A bare "user:" is a scope for nobody: it stores, it never resolves for any
	// user, and it shows on the overrides list as an override of nothing.
	return strings.HasPrefix(scope, "user:") && len(scope) > len("user:")
}

// validTrafficLabel accepts what the producers can actually emit. Both
// meterRegion implementations (edge and beam relay) strip everything outside
// [a-z0-9-], so a label with anything else can never match a counter - and a
// limit that can never match is indistinguishable, on screen, from one that
// works.
func validTrafficLabel(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

var (
	errBadLimitMode  = &limitError{"must be default, unlimited or custom"}
	errNegativeLimit = &limitError{"must not be negative"}
)

type limitError struct{ msg string }

func (e *limitError) Error() string { return e.msg }
