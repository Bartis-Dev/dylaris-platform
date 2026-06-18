package handlers

import (
	"dylaris-core/services"
	"encoding/json"
	"net/http"

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

// SetBillingStatus PATCH /api/admin/users/{id}/billing — admin transitions a
// tenant between active / past_due / suspended. past_due starts the grace window
// + dunning email; suspended stops servers; active reactivates (no auto-start).
func (h *BillingHandler) SetBillingStatus(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
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
		err = h.state.Billing.Suspend(r.Context(), userID)
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

// GetBillingSettings GET /api/admin/settings/billing — the platform default
// grace + retention windows and the payment URL the banner links to.
func (h *BillingHandler) GetBillingSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	get := func(key, def string) string {
		if v, _ := h.state.Store.GetSetting(key); v != "" {
			return v
		}
		return def
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"gracePeriod":   get(services.BillingGracePeriodKey, services.DefaultGracePeriod),
		"r2Retention":   get(services.BillingR2RetentionKey, services.DefaultR2Retention),
		"nodeRetention": get(services.BillingNodeRetentionKey, services.DefaultNodeRetention),
		"paymentUrl":    get(services.BillingPaymentURLKey, ""),
	})
}

// SetBillingSettings PUT /api/admin/settings/billing — write the platform
// defaults. Retention specs are validated; the payment URL is free-form.
func (h *BillingHandler) SetBillingSettings(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	var req struct {
		GracePeriod   string `json:"gracePeriod"`
		R2Retention   string `json:"r2Retention"`
		NodeRetention string `json:"nodeRetention"`
		PaymentUrl    string `json:"paymentUrl"`
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
	writes := map[string]string{
		services.BillingGracePeriodKey:   req.GracePeriod,
		services.BillingR2RetentionKey:   req.R2Retention,
		services.BillingNodeRetentionKey: req.NodeRetention,
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

// SetBillingOverrides PATCH /api/admin/users/{id}/billing-overrides — per-user
// retention overrides. An empty spec clears the override (falls back to the
// platform default).
func (h *BillingHandler) SetBillingOverrides(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Admin only", http.StatusForbidden)
		return
	}
	userID := mux.Vars(r)["id"]
	var req struct {
		GracePeriod   string `json:"gracePeriod"`
		R2Retention   string `json:"r2Retention"`
		NodeRetention string `json:"nodeRetention"`
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
	if err := h.state.Store.SetUserBillingOverrides(userID, req.GracePeriod, req.R2Retention, req.NodeRetention); err != nil {
		sendJSONError(w, "Save failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
