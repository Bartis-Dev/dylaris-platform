package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
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

	// Bind the token to uuid+email. Single-use is enforced by GetDel on verify.
	value := user.ID + "\n" + user.Email
	if err := h.state.Redis.Set(r.Context(), storeLinkTokenPrefix+token, value, storeLinkTokenTTL).Err(); err != nil {
		sendJSONError(w, "Failed to store token", http.StatusInternalServerError)
		return
	}

	redirectURL := strings.TrimRight(h.state.StoreURL, "/") + "/connect?token=" + url.QueryEscape(token)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "redirectUrl": redirectURL})
}

// LinkVerify POST /api/store/link/verify — store-key. Body {token}. Validates +
// consumes the token (single-use) and returns the bound {uuid, email}. Called
// by dylaris.com during the connect flow.
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
	parts := strings.SplitN(value, "\n", 2)
	if len(parts) != 2 {
		sendJSONError(w, "Malformed token", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"uuid":    parts[0],
		"email":   parts[1],
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
	endpoint := strings.TrimRight(h.state.StoreURL, "/") + "/api/store/link-status?uuid=" + url.QueryEscape(uuid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, ""
	}
	req.Header.Set("X-Store-Key", h.state.StoreSharedKey)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var parsed struct {
		Linked bool   `json:"linked"`
		Email  string `json:"email"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, ""
	}
	return parsed.Linked, parsed.Email
}
