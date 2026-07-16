package handlers

import (
	"dylaris-core/services"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

// BillingHandler exposes the BYON non-payment lifecycle: a tenant reads their own
// status (for the panel banner); an admin sets status + per-user retention
// overrides. Stripe/other webhooks will later call the same service methods.
type BillingHandler struct {
	state *AppState
}

func NewBillingHandler(state *AppState) *BillingHandler {
	return &BillingHandler{state: state}
}

// GetMyBilling GET /api/me/billing — the caller's lifecycle state for the banner.
// Always succeeds (zero = active).
func (h *BillingHandler) GetMyBilling(w http.ResponseWriter, r *http.Request) {
	userID := byonCallerID(r)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	b, err := h.state.Store.GetUserBilling(userID)
	if err != nil {
		sendJSONError(w, "Lookup failed", http.StatusInternalServerError)
		return
	}
	payment := ""
	if h.state.Billing != nil {
		payment = h.state.Billing.PaymentURL()
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"status":     b.Status,
		"graceUntil": b.GraceUntil,
		"paymentUrl": payment,
	})
}

// SetBillingStatus PATCH /api/admin/users/{id}/billing - RequireCap("plans.write")
// at the route. Admin transitions a tenant between active / past_due /
// suspended. past_due starts the grace window
// + dunning email; suspended is an IMMEDIATE force-suspend (SuspendNow: stops
// servers now and durably revokes every route-only link kit - they do NOT come
// back on reactivation, an admin must re-mint them); active reactivates (no
// auto-start, and only GRACED-suspended links restore automatically). The
// automatic non-payment lifecycle and the store webhook (handlers/store.go)
// keep the graced Suspend (deferred cutoff) - this admin path does not.
func (h *BillingHandler) SetBillingStatus(w http.ResponseWriter, r *http.Request) {
	if h.state.Billing == nil {
		sendJSONError(w, "Billing unavailable", http.StatusServiceUnavailable)
		return
	}
	userID := mux.Vars(r)["id"]
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	var err error
	switch req.Status {
	case "past_due":
		err = h.state.Billing.EnterPastDue(userID)
	case "active":
		err = h.state.Billing.Reactivate(userID)
	case "suspended":
		err = h.state.Billing.SuspendNow(r.Context(), userID)
	default:
		sendJSONError(w, "Invalid status (active|past_due|suspended)", http.StatusBadRequest)
		return
	}
	if err != nil {
		sendJSONError(w, "Update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": req.Status})
}

// GetBillingSettings GET /api/admin/settings/billing - RequireCap("plans.read")
// at the route. The platform default grace + retention windows and the
// payment URL the banner links to.
func (h *BillingHandler) GetBillingSettings(w http.ResponseWriter, r *http.Request) {
	get := func(key, def string) string {
		if v, _ := h.state.Store.GetSetting(key); v != "" {
			return v
		}
		return def
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"gracePeriod":       get(services.BillingGracePeriodKey, services.DefaultGracePeriod),
		"r2Retention":       get(services.BillingR2RetentionKey, services.DefaultR2Retention),
		"nodeRetention":     get(services.BillingNodeRetentionKey, services.DefaultNodeRetention),
		"r2QuotaGb":         get(services.BillingR2QuotaKey, "0"),
		"presignTtlNodeMin": get(services.PresignTTLNodeKey, strconv.Itoa(services.DefaultPresignTTLNodeMin)),
		"presignTtlByonMin": get(services.PresignTTLBYONKey, strconv.Itoa(services.DefaultPresignTTLBYONMin)),
		"paymentUrl":        get(services.BillingPaymentURLKey, ""),
	})
}

// SetBillingSettings PUT /api/admin/settings/billing - RequireCap("plans.write")
// at the route. Writes the platform defaults; retention specs are validated,
// the payment URL is free-form.
func (h *BillingHandler) SetBillingSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GracePeriod       string `json:"gracePeriod"`
		R2Retention       string `json:"r2Retention"`
		NodeRetention     string `json:"nodeRetention"`
		R2QuotaGb         string `json:"r2QuotaGb"`
		PresignTtlNodeMin string `json:"presignTtlNodeMin"`
		PresignTtlByonMin string `json:"presignTtlByonMin"`
		PaymentUrl        string `json:"paymentUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	for _, spec := range []string{req.GracePeriod, req.R2Retention, req.NodeRetention} {
		if !services.ValidRetentionSpec(spec) {
			sendJSONError(w, "Invalid retention spec (use e.g. 3d, 2w, 3m)", http.StatusBadRequest)
			return
		}
	}
	if req.R2QuotaGb == "" {
		req.R2QuotaGb = "0"
	}
	if n, err := strconv.ParseInt(req.R2QuotaGb, 10, 64); err != nil || n < 0 {
		sendJSONError(w, "R2 quota must be a non-negative number of GB (0 = unlimited)", http.StatusBadRequest)
		return
	}
	for _, ttl := range []string{req.PresignTtlNodeMin, req.PresignTtlByonMin} {
		if n, err := strconv.Atoi(ttl); err != nil || n <= 0 {
			sendJSONError(w, "Presigned URL TTL must be a positive number of minutes", http.StatusBadRequest)
			return
		}
	}
	writes := map[string]string{
		services.BillingGracePeriodKey:   req.GracePeriod,
		services.BillingR2RetentionKey:   req.R2Retention,
		services.BillingNodeRetentionKey: req.NodeRetention,
		services.BillingR2QuotaKey:       req.R2QuotaGb,
		services.PresignTTLNodeKey:       req.PresignTtlNodeMin,
		services.PresignTTLBYONKey:       req.PresignTtlByonMin,
		services.BillingPaymentURLKey:    req.PaymentUrl,
	}
	for k, v := range writes {
		if err := h.state.Store.SetSetting(k, v); err != nil {
			sendJSONError(w, "Save failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// SetBillingOverrides PATCH /api/admin/users/{id}/billing-overrides -
// RequireCap("plans.write") at the route. Per-user retention overrides; an
// empty spec clears the override (falls back to the platform default).
func (h *BillingHandler) SetBillingOverrides(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["id"]
	var req struct {
		GracePeriod   string `json:"gracePeriod"`
		R2Retention   string `json:"r2Retention"`
		NodeRetention string `json:"nodeRetention"`
		// R2QuotaGb is a pointer so null clears the override (use platform default)
		// while an explicit 0 means "unlimited for this user".
		R2QuotaGb *int64 `json:"r2QuotaGb"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	for _, spec := range []string{req.GracePeriod, req.R2Retention, req.NodeRetention} {
		if spec != "" && !services.ValidRetentionSpec(spec) {
			sendJSONError(w, "Invalid retention spec (use e.g. 3d, 2w, 3m)", http.StatusBadRequest)
			return
		}
	}
	if req.R2QuotaGb != nil && *req.R2QuotaGb < 0 {
		sendJSONError(w, "R2 quota must be a non-negative number of GB (0 = unlimited)", http.StatusBadRequest)
		return
	}
	if err := h.state.Store.SetUserBillingOverrides(userID, req.GracePeriod, req.R2Retention, req.NodeRetention, req.R2QuotaGb); err != nil {
		sendJSONError(w, "Save failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// GetUserBilling GET /api/admin/users/{id}/billing - RequireCap("plans.read")
// at the route. Reads a tenant's full lifecycle state (status + per-user
// retention overrides) plus the platform defaults so the override modal can
// show "uses default" hints. Like GetMyBilling it never 404s: a tenant with
// no row reads back as active with empty overrides.
func (h *BillingHandler) GetUserBilling(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["id"]
	b, err := h.state.Store.GetUserBilling(userID)
	if err != nil {
		sendJSONError(w, "Lookup failed", http.StatusInternalServerError)
		return
	}
	planID, _ := h.state.Store.GetUserPlanID(userID)
	get := func(key, def string) string {
		if v, _ := h.state.Store.GetSetting(key); v != "" {
			return v
		}
		return def
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"status":      b.Status,
		"graceUntil":  b.GraceUntil,
		"suspendedAt": b.SuspendedAt,
		"planId":      planID,
		"overrides": map[string]interface{}{
			"gracePeriod":       b.GracePeriod,
			"r2Retention":       b.R2Retention,
			"nodeRetention":     b.NodeRetention,
			"r2QuotaGb":         b.R2QuotaGB,
			"maxNodes":          b.MaxNodes,
			"maxLinks":          b.MaxLinks,
			"trafficEdgeGb":     b.TrafficEdgeGB,
			"trafficRelayGb":    b.TrafficRelayGB,
			"trafficCombinedGb": b.TrafficCombinedGB,
		},
		"defaults": map[string]interface{}{
			"gracePeriod":   get(services.BillingGracePeriodKey, services.DefaultGracePeriod),
			"r2Retention":   get(services.BillingR2RetentionKey, services.DefaultR2Retention),
			"nodeRetention": get(services.BillingNodeRetentionKey, services.DefaultNodeRetention),
			"r2QuotaGb":     get(services.BillingR2QuotaKey, "0"),
		},
	})
}
