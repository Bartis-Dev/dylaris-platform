package handlers

import (
	"dylaris-core/services"
	"dylaris-core/store"
	"encoding/json"
	"net/http"
	"time"
)

const usageGiB = int64(1024 * 1024 * 1024)

// usageView inlines a tenant's metered usage and adds their effective limits plus
// per-channel "over" warn flags (traffic is warn-only — these drive a UI banner,
// not a block). A 0 limit means unlimited, so it never flags.
type usageView struct {
	*store.TrafficUsage
	Limits services.Limits `json:"limits"`
	Over   map[string]bool `json:"over"`
}

func (h *UsageHandler) viewFor(u *store.TrafficUsage) usageView {
	lim, _ := services.EffectiveLimits(h.state.Store, u.UserID)
	over := func(limGB, bytes int64) bool { return limGB > 0 && bytes > limGB*usageGiB }
	return usageView{
		TrafficUsage: u,
		Limits:       lim,
		Over: map[string]bool{
			"edge":     over(lim.TrafficEdgeGB, u.EdgeBytes),
			"relay":    over(lim.TrafficRelayGB, u.RelayBytes),
			"combined": over(lim.TrafficCombinedGB, u.EdgeBytes+u.RelayBytes),
			"r2":       over(lim.R2QuotaGB, u.BackupBytes),
		},
	}
}

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
		"usage":   h.viewFor(u),
	})
}

// GetAllUsage GET /api/admin/usage - RequireCap("plans.read") at the route.
// Every tenant's usage for the period, busiest first.
func (h *UsageHandler) GetAllUsage(w http.ResponseWriter, r *http.Request) {
	period := usagePeriod(r)
	list, err := h.state.Store.ListTrafficUsage(period)
	if err != nil {
		sendJSONError(w, "Lookup failed", http.StatusInternalServerError)
		return
	}
	views := make([]usageView, 0, len(list))
	for i := range list {
		views = append(views, h.viewFor(&list[i]))
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"period":  period.Format("2006-01"),
		"usage":   views,
	})
}
