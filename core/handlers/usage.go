package handlers

import (
	"encoding/json"
	"net/http"
	"time"
)

// UsageHandler serves per-tenant traffic + storage usage for the BYON billing UI.
// In solo/hoster mode nothing is metered, so reads return zero rows.
type UsageHandler struct {
	state *AppState
}

func NewUsageHandler(state *AppState) *UsageHandler {
	return &UsageHandler{state: state}
}

// usagePeriod resolves the billing month from an optional ?period=YYYY-MM query,
// defaulting to the current UTC month (the aggregator's key).
func usagePeriod(r *http.Request) time.Time {
	if p := r.URL.Query().Get("period"); p != "" {
		if t, err := time.Parse("2006-01", p); err == nil {
			return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		}
	}
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// GetMyUsage GET /api/me/usage — the caller's metered usage for the period.
// Returns a zero-value row when nothing has been metered yet.
func (h *UsageHandler) GetMyUsage(w http.ResponseWriter, r *http.Request) {
	userID := byonCallerID(r)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	u, err := h.state.Store.GetTrafficUsage(userID, usagePeriod(r))
	if err != nil {
		sendJSONError(w, "Lookup failed", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"usage":   u,
	})
}

// GetAllUsage GET /api/admin/usage — every tenant's usage for the period, busiest
// first. Admin only.
func (h *UsageHandler) GetAllUsage(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	period := usagePeriod(r)
	list, err := h.state.Store.ListTrafficUsage(period)
	if err != nil {
		sendJSONError(w, "Lookup failed", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"period":  period.Format("2006-01"),
		"usage":   list,
	})
}
