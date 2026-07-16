package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"dylaris-core/authz"
	"dylaris-core/models"

	"github.com/gorilla/mux"
)

// API key surface. Owner manages keys per-account. Each key has a
// scope (server UUIDs + permission names). The external RCON middleware
// validates Authorization: Bearer <key> against this store.

type APIKeysHandler struct {
	state *AppState
	// rateLimiter is a per-key sliding-window counter, in-memory. Acceptable
	// for V1 — Multi-Core deployments share the lease but not the limiter;
	// the only real risk is "2x burst" if a script flaps between Cores. A
	// distributed Redis-backed limiter is wired in when that matters.
	rateLimiter *apiKeyRateLimiter
	// ipLimiter throttles per source IP BEFORE the key lookup so invalid-key
	// guessing can't hammer the DB hash lookup unbounded.
	ipLimiter *IPRateLimiter
}

func NewAPIKeysHandler(state *AppState) *APIKeysHandler {
	return &APIKeysHandler{state: state, rateLimiter: newAPIKeyRateLimiter(), ipLimiter: NewIPRateLimiter()}
}

type createAPIKeyRequest struct {
	Name        string   `json:"name"`
	Servers     []string `json:"servers"`
	Permissions []string `json:"permissions"`
	RatePerMin  int      `json:"ratePerMin,omitempty"`
}

type createAPIKeyResponse struct {
	Success   bool          `json:"success"`
	APIKey    *models.APIKey `json:"apiKey"`
	Plaintext string        `json:"plaintext"`
	Message   string        `json:"message"`
}

// validPermissions pins the capability vocabulary; later phases extend this
// (modpack.publish, server.power.start, ...).
var validPermissions = map[string]bool{
	"rcon.exec": true,
}

func (h *APIKeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req createAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		sendJSONError(w, "Name required", http.StatusBadRequest)
		return
	}
	if len(req.Permissions) == 0 {
		sendJSONError(w, "At least one permission required", http.StatusBadRequest)
		return
	}
	for _, p := range req.Permissions {
		if !validPermissions[p] {
			sendJSONError(w, "Unknown permission: "+p, http.StatusBadRequest)
			return
		}
	}
	// Non-admin callers can only scope keys to servers they themselves can power-access.
	isAdmin := r.Context().Value("isAdmin").(bool)
	if !isAdmin {
		for _, serverUUID := range req.Servers {
			srv, err := h.state.Store.GetServerByUUID(serverUUID)
			if err != nil {
				sendJSONError(w, "Server not found: "+serverUUID, http.StatusBadRequest)
				return
			}
			username := r.Context().Value("username").(string)
			if !checkServerAccess(h.state.Store, srv, username, false, userID, "power") {
				sendJSONError(w, "No power access to server: "+serverUUID, http.StatusForbidden)
				return
			}
		}
	}

	plaintext, err := generatePlaintextKey()
	if err != nil {
		sendJSONError(w, "Failed to mint key", http.StatusInternalServerError)
		return
	}
	rate := req.RatePerMin
	if rate <= 0 {
		rate = 60
	}
	k := &models.APIKey{
		UserID:  userID,
		Name:    req.Name,
		KeyHash: HashAPIKey(plaintext),
		Scope: models.APIKeyScope{
			Servers:     req.Servers,
			Permissions: req.Permissions,
		},
		RatePerMin: rate,
	}
	id, err := h.state.Store.CreateAPIKey(k)
	if err != nil {
		sendJSONError(w, "Failed to save key", http.StatusInternalServerError)
		return
	}
	k.ID = id
	json.NewEncoder(w).Encode(createAPIKeyResponse{
		Success:   true,
		APIKey:    k,
		Plaintext: "dyl_" + plaintext,
		Message:   "Store this key now — it will not be shown again.",
	})
}

func (h *APIKeysHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	keys, err := h.state.Store.ListAPIKeysByUser(userID)
	if err != nil {
		sendJSONError(w, "Failed to list keys", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"keys":    keys,
	})
}

func (h *APIKeysHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("userID").(string)
	if userID == "" {
		sendJSONError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	keyID, _ := strconv.Atoi(mux.Vars(r)["id"])
	if err := h.state.Store.RevokeAPIKey(keyID, userID); err != nil {
		sendJSONError(w, "Key not found", http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func generatePlaintextKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateLinkIdentity mints a stable, unique link identity (the warp key's
// node_id). The Link derives its tunnel token deterministically from this id +
// the cluster secret, so the id must be unguessable. 16 random bytes suffice.
func generateLinkIdentity() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "link-" + hex.EncodeToString(b), nil
}

// --- External RCON middleware ---

// APIKeyMiddleware validates Authorization: Bearer <dyl_…> for the external
// surface. On success it injects the resolved APIKey into the context.
func (h *APIKeysHandler) APIKeyMiddleware(requiredPerm string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				sendJSONError(w, "Missing Authorization", http.StatusUnauthorized)
				return
			}
			plaintext := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			plaintext = strings.TrimPrefix(plaintext, "dyl_")
			if plaintext == "" {
				sendJSONError(w, "Invalid key", http.StatusUnauthorized)
				return
			}
			// Throttle by source IP before the DB lookup so invalid-key brute
			// force can't hammer GetAPIKeyByHash unbounded (the per-key limiter
			// below never gets a chance to run for keys that don't exist).
			if !h.ipLimiter.allow(clientIP(r), 120) {
				w.Header().Set("Retry-After", "60")
				sendJSONError(w, "Too many requests", http.StatusTooManyRequests)
				return
			}
			key, err := h.state.Store.GetAPIKeyByHash(HashAPIKey(plaintext))
			if err != nil {
				sendJSONError(w, "Invalid key", http.StatusUnauthorized)
				return
			}
			if key.RevokedAt != nil {
				sendJSONError(w, "Key revoked", http.StatusUnauthorized)
				return
			}
			if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
				sendJSONError(w, "Key expired", http.StatusUnauthorized)
				return
			}
			// Server-UUID scope: when the path carries a {uuid} the key must be scoped
			// to it. serverAllowed then gates whether the key's SERVER caps count for
			// this request when it is turned into a Resolution below.
			uuidVar := mux.Vars(r)["uuid"]
			serverAllowed := uuidVar == "" || key.Scope.AllowsServer(uuidVar)
			if uuidVar != "" && !serverAllowed {
				sendJSONError(w, "Key not scoped to this server", http.StatusForbidden)
				return
			}
			// Authorization through the SAME chokepoint as session auth: the key's caps
			// become a Resolution and the required cap is checked via HasCap. A key
			// holds exactly its minted caps - no admin/owner short-circuit, no panel
			// caps - so this generalizes enforcement to any SERVER/OWNER cap, not just
			// rcon.exec, while denying anything the key does not carry.
			if requiredPerm != "" {
				if !authz.ResolveAPIKey(key.Scope.Permissions, serverAllowed).HasCap(requiredPerm) {
					sendJSONError(w, "Key lacks required permission", http.StatusForbidden)
					return
				}
			}
			if !h.rateLimiter.allow(key.ID, key.RatePerMin) {
				w.Header().Set("Retry-After", "60")
				sendJSONError(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			// Best-effort last-used stamp, throttled to ~once/min/key so a
			// hammered key can't spawn an unbounded goroutine fan-out.
			if h.rateLimiter.shouldTouch(key.ID) {
				go h.state.Store.TouchAPIKey(key.ID)
			}
			next(w, r)
		}
	}
}

// --- Rate limiter ---

type rateBucket struct {
	count int
	reset time.Time
}

type apiKeyRateLimiter struct {
	mu        sync.Mutex
	buckets   map[int]*rateBucket
	lastTouch map[int]time.Time
}

func newAPIKeyRateLimiter() *apiKeyRateLimiter {
	return &apiKeyRateLimiter{buckets: make(map[int]*rateBucket), lastTouch: make(map[int]time.Time)}
}

// shouldTouch returns true at most once per minute per key, gating the
// last-used DB stamp so a hot key doesn't spawn a goroutine + UPDATE on
// every request.
func (l *apiKeyRateLimiter) shouldTouch(keyID int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if t, ok := l.lastTouch[keyID]; ok && now.Sub(t) < time.Minute {
		return false
	}
	l.lastTouch[keyID] = now
	return true
}

// allow returns false when the per-key budget is exhausted for the current
// 60-second window. Cheap sliding-window-counter: roll the bucket on each
// call when its reset has passed.
func (l *apiKeyRateLimiter) allow(keyID, perMin int) bool {
	if perMin <= 0 {
		perMin = 60
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[keyID]
	now := time.Now()
	if !ok || now.After(b.reset) {
		l.buckets[keyID] = &rateBucket{count: 1, reset: now.Add(time.Minute)}
		return true
	}
	if b.count >= perMin {
		return false
	}
	b.count++
	return true
}
