package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Store-linking: the bridge between the platform Core (source of truth for the
// real user) and the dylaris.com storefront (passwordless accounts + billing).
// The two are decoupled; the only join is the Core user UUID, which the store
// holds (single-owned link). Core never stores a copy — it reads "linked" status
// from the store on demand.
//
// Trust model (two layers):
//   - STORE_SHARED_KEY = service-to-service trust (this is my partner store),
//     carried in the X-Store-Key header on store<->core calls. NOT a user proof.
//   - The link-token = user proof. Core mints a single-use, short-TTL token bound
//     to the authenticated panel user's UUID+email, then redirects the browser to
//     the store. The store exchanges the token (over the shared key) for {uuid,
//     email} and asks the user to confirm before persisting the link.

const (
	storeLinkTokenPrefix = "dylaris:store:linktoken:"
	storeLinkTokenTTL    = 10 * time.Minute
)

// StoreHandler serves the core-side store-linking endpoints.
type StoreHandler struct {
	state *AppState
}

func NewStoreHandler(state *AppState) *StoreHandler {
	return &StoreHandler{state: state}
}

// requireStoreKey enforces the service-to-service shared key (constant-time) and
// that the store integration is enabled. Returns true when the request may
// proceed; otherwise it writes the error response and returns false.
func (h *StoreHandler) requireStoreKey(w http.ResponseWriter, r *http.Request) bool {
	if !h.state.StoreEnabled {
		sendJSONError(w, "Store integration not enabled", http.StatusNotFound)
		return false
	}
	got := r.Header.Get("X-Store-Key")
	want := h.state.StoreSharedKey
	if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		sendJSONError(w, "Invalid store key", http.StatusUnauthorized)
		return false
	}
	return true
}

// LinkStart POST /api/store/link/start — authed panel user. Mints a single-use,
// short-TTL token bound to the caller's UUID+email and returns the storefront
// redirect URL. The browser then lands on dylaris.com/connect?token=...
func (h *StoreHandler) LinkStart(w http.ResponseWriter, r *http.Request) {
	if !h.state.StoreEnabled {
		sendJSONError(w, "Store integration not enabled", http.StatusNotFound)
		return
	}
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	user, err := h.state.Store.GetUserByID(userID)
	if err != nil || user == nil {
		sendJSONError(w, "User not found", http.StatusNotFound)
		return
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		sendJSONError(w, "Failed to mint token", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(tokenBytes)

	// Bind the token to uuid+email+username. Single-use is enforced by GetDel on
	// verify. Newline-delimited; verify tolerates an absent username (old tokens).
	value := user.ID + "\n" + user.Email + "\n" + user.Username
	if err := h.state.Redis.Set(r.Context(), storeLinkTokenPrefix+token, value, storeLinkTokenTTL).Err(); err != nil {
		sendJSONError(w, "Failed to store token", http.StatusInternalServerError)
		return
	}

	redirectURL := strings.TrimRight(h.state.StoreURL, "/") + "/connect?token=" + url.QueryEscape(token)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "redirectUrl": redirectURL})
}

// LinkVerify POST /api/store/link/verify — store-key. Body {token}. Validates +
// consumes the token (single-use) and returns the bound {uuid, email, username}
// plus this Core's own panel URL. Called by dylaris.com during the connect flow.
//
// panelUrl travels HERE rather than as a query parameter on the /connect link,
// which is the obvious alternative and the wrong one: the storefront sends the
// browser back to it after linking, so a value taken from the URL would be an
// open redirect anyone could aim anywhere. Coming back over the store-key
// channel it is attested by the same Core that minted the token, and there is
// nothing for a visitor to tamper with.
func (h *StoreHandler) LinkVerify(w http.ResponseWriter, r *http.Request) {
	if !h.requireStoreKey(w, r) {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		sendJSONError(w, "Invalid token", http.StatusBadRequest)
		return
	}

	// GetDel = atomic read-and-consume, so a token can never be redeemed twice.
	value, err := h.state.Redis.GetDel(r.Context(), storeLinkTokenPrefix+req.Token).Result()
	if err != nil || value == "" {
		sendJSONError(w, "Invalid or expired token", http.StatusUnauthorized)
		return
	}
	parts := strings.SplitN(value, "\n", 3)
	if len(parts) < 2 {
		sendJSONError(w, "Malformed token", http.StatusInternalServerError)
		return
	}
	username := ""
	if len(parts) == 3 {
		username = parts[2]
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"uuid":     parts[0],
		"email":    parts[1],
		"username": username,
		// Empty when FRONTEND_URL is unset; the storefront then simply offers no
		// way back and says so, rather than guessing an origin.
		"panelUrl": strings.TrimRight(h.state.FrontendURL, "/"),
	})
}

// VerifyUser GET /api/store/verify-user?uuid= — store-key. The purchase-gate
// doppelcheck: dylaris.com calls this right before charging to confirm the
// linked Core user still exists.
func (h *StoreHandler) VerifyUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireStoreKey(w, r) {
		return
	}
	uuid := strings.TrimSpace(r.URL.Query().Get("uuid"))
	if uuid == "" {
		sendJSONError(w, "Missing uuid", http.StatusBadRequest)
		return
	}
	user, err := h.state.Store.GetUserByID(uuid)
	if err != nil || user == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "exists": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"exists":   true,
		"uuid":     user.ID,
		"username": user.Username,
	})
}

// GetUsage GET /api/store/usage?uuid= — store-key. Returns the linked tenant's
// metered traffic for the current billing month so the store can compute overage
// and bill it. edge_bytes is the billable player traffic; the rest are for
// observability. A tenant with no traffic yet returns zeros (never an error).
func (h *StoreHandler) GetUsage(w http.ResponseWriter, r *http.Request) {
	if !h.requireStoreKey(w, r) {
		return
	}
	uuid := strings.TrimSpace(r.URL.Query().Get("uuid"))
	if uuid == "" {
		sendJSONError(w, "Missing uuid", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	period := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	usage, err := h.state.Store.GetTrafficUsage(uuid, period)
	if err != nil {
		sendJSONError(w, "Failed to read usage", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"period":      period.Format("2006-01-02"),
		"edgeBytes":   usage.EdgeBytes,
		"relayBytes":  usage.RelayBytes,
		"backupBytes": usage.BackupBytes,
	})
}

// Provision POST /api/store/provision — store-key. The store (source of truth
// for payment) drives the Core billing lifecycle for a linked tenant:
//
//	action "activate" -> billing active (+ optional plan assignment)
//	action "past_due" -> dunning grace window starts
//	action "suspend"  -> mark suspended now; servers stop + route-only links
//	                     are torn down after the grace window elapses (no data
//	                     deleted). Deliberately graced, not immediate: a buggy
//	                     webhook must not instant-kill a paying tenant. Compare
//	                     the admin-manual path (handlers/billing.go), which
//	                     uses the immediate SuspendNow instead.
//
// This is how a successful Stripe checkout / failed payment / canceled
// subscription reaches Core. It NEVER deletes user data.
func (h *StoreHandler) Provision(w http.ResponseWriter, r *http.Request) {
	if !h.requireStoreKey(w, r) {
		return
	}
	if h.state.Billing == nil {
		sendJSONError(w, "Billing not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		UUID   string `json:"uuid"`
		Action string `json:"action"`
		PlanID *int   `json:"planId,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UUID == "" {
		sendJSONError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	user, err := h.state.Store.GetUserByID(req.UUID)
	if err != nil || user == nil {
		sendJSONError(w, "User not found", http.StatusNotFound)
		return
	}

	switch req.Action {
	case "activate":
		if err := h.state.Billing.Reactivate(req.UUID); err != nil {
			sendJSONError(w, "Failed to activate billing", http.StatusInternalServerError)
			return
		}
		if req.PlanID != nil && *req.PlanID > 0 {
			if err := h.state.Store.SetUserPlan(req.UUID, req.PlanID); err != nil {
				sendJSONError(w, "Activated but failed to assign plan", http.StatusInternalServerError)
				return
			}
		}
	case "past_due":
		if err := h.state.Billing.EnterPastDue(req.UUID); err != nil {
			sendJSONError(w, "Failed to set past_due", http.StatusInternalServerError)
			return
		}
	case "suspend":
		if err := h.state.Billing.Suspend(r.Context(), req.UUID); err != nil {
			sendJSONError(w, "Failed to suspend", http.StatusInternalServerError)
			return
		}
	default:
		sendJSONError(w, "Unknown action", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "action": req.Action})
}

// Status GET /api/store/status — authed panel user. Reads the link status from
// dylaris.com on demand (single-owned link: the store holds the join, Core does
// not). Drives the account-settings "Connect Store" / "Linked" UI.
func (h *StoreHandler) Status(w http.ResponseWriter, r *http.Request) {
	if !h.state.StoreEnabled {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "enabled": false, "linked": false})
		return
	}
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	linked, email := h.fetchLinkStatus(r.Context(), userID)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"enabled":  true,
		"linked":   linked,
		"email":    email,
		"storeUrl": h.state.StoreURL,
	})
}

// fetchLinkStatus asks dylaris.com whether this Core UUID is linked. Best-effort:
// on any store-side error it reports "not linked" rather than failing the panel.
func (h *StoreHandler) fetchLinkStatus(ctx context.Context, uuid string) (bool, string) {
	linked, email, err := h.probeLinkStatus(ctx, uuid)
	if err != nil {
		// Still fail soft for the customer - a store outage must not break the
		// panel - but say so somewhere. Every one of these used to return a bare
		// false, so a wrong STORE_SHARED_KEY, an unreachable storefront and a
		// genuinely unlinked account produced the identical "Connect Store"
		// button. The one state that needs an operator was indistinguishable
		// from the two that do not.
		log.Printf("store: link-status lookup failed for %s: %v", uuid, err)
		return false, ""
	}
	return linked, email
}

// probeLinkStatus is fetchLinkStatus with the error kept, so the health check can
// report WHAT is wrong with the storefront integration instead of the panel
// silently rendering "not linked".
func (h *StoreHandler) probeLinkStatus(ctx context.Context, uuid string) (bool, string, error) {
	endpoint := strings.TrimRight(h.state.StoreURL, "/") + "/api/store/link-status?uuid=" + url.QueryEscape(uuid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, "", err
	}
	req.Header.Set("X-Store-Key", h.state.StoreSharedKey)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("cannot reach the storefront at %s: %w", h.state.StoreURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// The single most likely misconfiguration, and the one that looks most
		// like normal operation from the panel.
		return false, "", fmt.Errorf("the storefront refused our key (HTTP %d) - STORE_SHARED_KEY does not match the value configured on %s", resp.StatusCode, h.state.StoreURL)
	}
	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("the storefront answered HTTP %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var parsed struct {
		Linked bool   `json:"linked"`
		Email  string `json:"email"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, "", fmt.Errorf("the storefront answered something that is not the expected JSON: %w", err)
	}
	return parsed.Linked, parsed.Email, nil
}
