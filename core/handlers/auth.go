package handlers

import (
	"context"
	"database/sql" // Import Models
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"dylaris-core/store"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type AuthHandler struct {
	state  *AppState
	jwtKey []byte
}

func NewAuthHandler(state *AppState, jwtSecret string) *AuthHandler {
	return &AuthHandler{state: state, jwtKey: []byte(jwtSecret)}
}

// IssueToken signs a standard 24h session JWT for the given user. Extracted
// from the login handler so other handlers (the setup wizard, future
// admin-impersonation flows) can mint tokens without duplicating the signing
// + claim shape.
func (h *AuthHandler) IssueToken(username string, isAdmin bool) (string, error) {
	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		Username:         username,
		IsAdmin:          isAdmin,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(expirationTime)},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(h.jwtKey)
}

// DemoLogin POST /api/auth/demo-login — public, rate-limited.
// Issues a normal session for the designated read-only demo account, so the
// website/panel can offer a one-click "View demo" with no credentials. The
// account is forced GET-only by AuthMiddleware, so the session can only view.
// Returns 404 when no demo account is configured (feature off).
func (h *AuthHandler) DemoLogin(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", http.StatusServiceUnavailable)
		return
	}
	uuid, _ := h.state.Store.GetSetting(demoAccountUUIDSetting)
	if uuid == "" {
		sendJSONError(w, "Demo is not enabled", http.StatusNotFound)
		return
	}
	u, err := h.state.Store.GetUserByID(uuid)
	if err != nil || u == nil {
		sendJSONError(w, "Demo account unavailable", http.StatusNotFound)
		return
	}
	token, err := h.IssueToken(u.Username, false)
	if err != nil {
		sendJSONError(w, "Failed to issue token", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "token": token, "username": u.Username})
}

// ... Structs LoginRequest, Claims, UpdateRequest etc. kept as-is ...
// Claims.Purpose distinguishes normal sessions ("" / "session") from
// short-lived special-purpose tokens. "2fa_setup" is a token issued at
// login when the policy demands 2FA and the user hasn't configured it
// yet — accepted only by /auth/2fa/setup and /auth/2fa/verify so the
// user can finish enrollment, nothing else.
type Claims struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
	Purpose  string `json:"purpose,omitempty"`
	jwt.RegisteredClaims
}

// purposes whitelisted for setup-token JWTs. Anything else gets 403'd
// by AuthMiddleware so the bearer of a setup token can't access regular
// endpoints just because they have a valid signature.
var setupTokenAllowedPaths = map[string]bool{
	"/api/auth/2fa/setup":  true,
	"/api/auth/2fa/verify": true,
	// Used by the forced-setup page to load the user's username.
	"/api/auth/profile": true,
}
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTPCode string `json:"totpCode,omitempty"` // 6-digit TOTP or 8-char backup code
}
type UpdateRequest struct {
	OldPassword       string  `json:"oldPassword"`
	NewUsername       *string `json:"newUsername,omitempty"`
	NewPassword       *string `json:"newPassword,omitempty"`
	MinecraftUsername *string `json:"minecraftUsername,omitempty"`
	Email             *string `json:"email,omitempty"`
	// Note: 2FA is NOT toggled via this endpoint — use /auth/2fa/setup + /auth/2fa/verify
	// (or /auth/2fa/disable) so the user must prove possession of the secret.
}

func (h *AuthHandler) AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		// SSE auth ticket: EventSource cannot set an Authorization header, so
		// the panel mints a short-lived random ticket (POST /api/sse-ticket)
		// and carries it in the URL instead of the session JWT — the JWT must
		// never appear in URLs (access logs / Referer). Confined to GET. A
		// valid ticket resolves to a username in Redis, and we re-derive the
		// full identity (incl. isAdmin) from the DB so nothing here is
		// client-supplied. The ticket TTL is refreshed on every accepted
		// request, giving a sliding window so native EventSource reconnects
		// keep working with the same ?ticket=.
		if authHeader == "" && r.Method == http.MethodGet && h.state.Redis != nil {
			if ticket := r.URL.Query().Get("ticket"); ticket != "" {
				key := "sse:ticket:" + ticket
				if username, err := h.state.Redis.Get(r.Context(), key).Result(); err == nil && username != "" {
					w.Header().Set("Referrer-Policy", "no-referrer")
					w.Header().Set("Cache-Control", "no-store")
					// Sliding window: keep long-lived streams alive.
					h.state.Redis.Expire(r.Context(), key, sseTicketTTL)

					isAdmin := false
					var userID interface{}
					if h.state.Store != nil {
						if user, err := h.state.Store.GetUserByUsername(username); err == nil && user != nil {
							isAdmin = user.IsAdmin
							userID = user.ID
						}
					}
					ctx := context.WithValue(r.Context(), "username", username)
					ctx = context.WithValue(ctx, "isAdmin", isAdmin)
					if userID != nil {
						ctx = context.WithValue(ctx, "userID", userID)
					}
					next(w, r.WithContext(ctx))
					return
				}
				// Invalid/expired ticket: fall through to the logic below,
				// which 401s (no Authorization header, no ?token=).
			}
		}
		// Fallback: EventSource (SSE) cannot send custom headers, so accept a
		// ?token= query param. Confine it to GET (SSE + downloads) — a mutating
		// verb never legitimately authenticates via the URL — and when used,
		// stop the token from leaking onward: no-referrer keeps it out of the
		// Referer header if the page navigates away, no-store keeps it out of
		// shared caches/proxies.
		if authHeader == "" {
			if qToken := r.URL.Query().Get("token"); qToken != "" && r.Method == http.MethodGet {
				authHeader = "Bearer " + qToken
				w.Header().Set("Referrer-Policy", "no-referrer")
				w.Header().Set("Cache-Control", "no-store")
			}
		}
		if authHeader == "" {
			sendJSONError(w, "Missing Authorization Header", http.StatusUnauthorized)
			return
		}
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return h.jwtKey, nil
		}, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !token.Valid {
			sendJSONError(w, "Invalid Token", http.StatusUnauthorized)
			return
		}

		// Setup tokens are scoped to a tiny allowlist. Any other endpoint
		// must reject them with 403 — bearer doesn't have a full session yet.
		if claims.Purpose == "2fa_setup" {
			if !setupTokenAllowedPaths[r.URL.Path] {
				sendJSONError(w, "Token is restricted to 2FA setup — finish enrollment first", http.StatusForbidden)
				return
			}
		}

		ctx := context.WithValue(r.Context(), "username", claims.Username)
		ctx = context.WithValue(ctx, "isAdmin", claims.IsAdmin)

		// Resolve userID from DB for invite checks
		if h.state.Store != nil {
			if user, err := h.state.Store.GetUserByUsername(claims.Username); err == nil {
				ctx = context.WithValue(ctx, "userID", user.ID)
				// The demo account is read-only: reject every mutating verb so a
				// public demo session can only ever view, never change anything.
				// One central gate covers all write endpoints (power, files, RCON,
				// profile, server-create, ...) without per-handler checks.
				if !claims.IsAdmin && isDemoAccount(h.state.Store, user.ID) &&
					r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
					sendJSONError(w, "The demo account is read-only", http.StatusForbidden)
					return
				}
			}
		}

		next(w, r.WithContext(ctx))
	}
}

// IsAdminToken reports whether the request carries a valid token whose claims
// mark an admin. Used by middleware that runs before AuthMiddleware (the
// maintenance gate) to honor the admin bypass without a full auth pass.
func (h *AuthHandler) IsAdminToken(r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		if q := r.URL.Query().Get("token"); q != "" {
			authHeader = "Bearer " + q
		}
	}
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	if tokenString == "" {
		return false
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		return h.jwtKey, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !token.Valid {
		return false
	}
	return claims.IsAdmin
}

func (h *AuthHandler) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", http.StatusServiceUnavailable)
		return
	}

	var req LoginRequest
	json.NewDecoder(r.Body).Decode(&req)

	// STORE REFACTOR: No more raw SQL queries!
	user, err := h.state.Store.GetUserByUsername(req.Username)

	if err == sql.ErrNoRows {
		sendJSONError(w, "User not found", http.StatusUnauthorized)
		return
	} else if err != nil {
		if dbUnavailable(err) {
			sendJSONError(w, "The database is currently unavailable — please try again in a moment.", http.StatusServiceUnavailable)
			return
		}
		sendJSONError(w, "Database Error", http.StatusInternalServerError)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		sendJSONError(w, "Invalid Password", http.StatusUnauthorized)
		return
	}

	// Email-verify gate: if the server requires verification and
	// this user hasn't verified yet, block login with a distinctive flag so
	// the UI can offer a "resend verification email" button instead of a
	// generic auth error. Admins still get to log in even when unverified —
	// otherwise a misconfigured SMTP would lock everyone out.
	policy := LoadAuthPolicy(h.state)
	if policy.EmailVerifyRequired && !user.IsAdmin && user.EmailVerifiedAt == nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":              false,
			"requiresVerification": true,
			"email":                user.Email,
			"message":              "Please verify your email address before signing in.",
		})
		return
	}

	// 2FA enforcement gate: if the policy requires 2FA for this
	// user (admin or everyone) and they haven't enrolled, issue a *setup token*
	// — a short-lived JWT scoped exclusively to the 2FA setup endpoints —
	// instead of a normal session. Forces enrollment before they can use the
	// panel. Admins enforcing 2FA on themselves still works: they get the
	// setup token like anyone else.
	if !user.Is2FAEnabled {
		needs2FA := (user.IsAdmin && policy.Require2FAForAdmins) || policy.Require2FAForAllUsers
		if needs2FA {
			setupExp := time.Now().Add(15 * time.Minute)
			setupClaims := &Claims{
				Username:         user.Username,
				IsAdmin:          user.IsAdmin,
				Purpose:          "2fa_setup",
				RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(setupExp)},
			}
			tok := jwt.NewWithClaims(jwt.SigningMethodHS256, setupClaims)
			setupTokenString, _ := tok.SignedString(h.jwtKey)
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":           false,
				"requires2FASetup":  true,
				"setupToken":        setupTokenString,
				"setupTokenExpires": setupExp.Unix(),
				"message":           "Two-factor authentication is required for this account. Finish enrollment to continue.",
			})
			return
		}
	}

	// 2FA gate: if the user has 2FA enabled, require a valid TOTP or backup code.
	if user.Is2FAEnabled {
		if req.TOTPCode == "" {
			// Signal to the frontend that a code is needed without leaking
			// whether the password was correct (it was — but we still
			// expose this fact since 2FA is the user's chosen second factor).
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":     false,
				"requires2FA": true,
				"message":     "2FA code required",
			})
			return
		}
		ok, err := h.verifyTOTPOrBackup(user, req.TOTPCode)
		if err != nil {
			sendJSONError(w, "2FA verification failed", http.StatusInternalServerError)
			return
		}
		if !ok {
			sendJSONError(w, "Invalid 2FA code", http.StatusUnauthorized)
			return
		}
	}

	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		Username:         user.Username,
		IsAdmin:          user.IsAdmin,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(expirationTime)},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString(h.jwtKey)

	// Best-effort last-login stamp. Auto-delete reads this to distinguish
	// active vs dormant users — a stale value here is harmless.
	if err := h.state.Store.UpdateLastLoginAt(user.ID); err != nil {
		// Non-fatal — log and continue so a slow DB write never blocks login.
		log.Printf("login: UpdateLastLoginAt for userID=%s failed: %v", user.ID, err)
	}

	// Auto-rescue: if the user was already in pending_deletion
	// state, cancel the scheduled deletion silently. The fact that they
	// logged in is the strongest possible signal that the account is alive.
	if user.DeletionStatus == "pending_deletion" {
		if err := h.state.Store.CancelUserDeletion(user.ID); err == nil {
			LogIdentityAudit(h.state, r, AuditEventDeletionCancelledAtLogin, user.ID, user.ID, nil)
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"token":   tokenString,
		"isAdmin": user.IsAdmin,
	})
}

// dbUnavailable reports whether err looks like the database being
// unreachable or not yet ready, rather than a genuine query failure.
// Both the connection being down and the schema not being rebuilt yet
// (the schema-heal loop recreates it) are transient — the right
// user-facing response is "try again shortly", not a hard 500.
func dbUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, n := range []string{
		"connection refused", "dial tcp", "no such host",
		"bad connection", "connection reset", "broken pipe",
		"i/o timeout", "server closed the connection",
		"the database system is", // starting up / shutting down / in recovery
		"does not exist",          // schema not rebuilt yet
	} {
		if strings.Contains(msg, n) {
			return true
		}
	}
	return false
}

func (h *AuthHandler) GetProfileHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		return
	}
	username := r.Context().Value("username").(string)

	// STORE REFACTOR
	user, err := h.state.Store.GetUserByUsername(username)
	if err != nil {
		sendJSONError(w, "User not found", http.StatusNotFound)
		return
	}

	// Clear password before sending
	user.Password = ""
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "user": user})
}

func (h *AuthHandler) UpdateProfileHandler(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		return
	}
	username := r.Context().Value("username").(string)
	isAdmin, _ := r.Context().Value("isAdmin").(bool)

	var req UpdateRequest
	json.NewDecoder(r.Body).Decode(&req)

	user, err := h.state.Store.GetUserByUsername(username)
	if err != nil {
		sendJSONError(w, "User not found", 404)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.OldPassword)); err != nil {
		sendJSONError(w, "Invalid current password", 401)
		return
	}

	// Username change: route through RenameUser with policy + cooldown + uniqueness guards.
	// RenameUser writes user_username_history and bumps last_username_change in one tx, so
	// we MUST NOT also write the username column in the generic UpdateUser call below.
	if req.NewUsername != nil && strings.TrimSpace(*req.NewUsername) != user.Username {
		newName := strings.TrimSpace(*req.NewUsername)
		if newName == "" {
			sendJSONError(w, "Username cannot be empty", http.StatusBadRequest)
			return
		}
		allow, cooldownDays, _ := h.state.Store.GetUserAccountPolicy()
		if !allow && !isAdmin {
			sendJSONError(w, "Username changes are disabled by the admin", http.StatusForbidden)
			return
		}
		if !isAdmin && user.LastUsernameChange != nil {
			earliest := user.LastUsernameChange.Add(time.Duration(cooldownDays) * 24 * time.Hour)
			if time.Now().Before(earliest) {
				w.Header().Set("Retry-After", earliest.UTC().Format(time.RFC3339))
				sendJSONError(w, "Username cooldown active; try again on "+earliest.Format("2006-01-02"), http.StatusTooManyRequests)
				return
			}
		}
		if existing, _ := h.state.Store.GetUserByUsername(newName); existing != nil && existing.ID != user.ID {
			sendJSONError(w, "Username already taken", http.StatusConflict)
			return
		}
		if err := h.state.Store.RenameUser(user.ID, newName, user.ID); err != nil {
			// The pre-check above is best-effort UX; the username UNIQUE
			// constraint is the real guard against a concurrent rename race.
			if errors.Is(err, store.ErrUsernameTaken) {
				sendJSONError(w, "Username already taken", http.StatusConflict)
				return
			}
			sendJSONError(w, "Rename failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		h.state.Events.Publish(r.Context(), "users.changed", map[string]interface{}{"userId": user.ID})
		user.Username = newName
	}

	// Update Fields in Struct (non-username fields)
	if req.NewPassword != nil && *req.NewPassword != "" {
		hashed, _ := bcrypt.GenerateFromPassword([]byte(*req.NewPassword), bcrypt.DefaultCost)
		user.Password = string(hashed)
	}
	if req.Email != nil {
		user.Email = *req.Email
	}
	if req.MinecraftUsername != nil {
		user.MinecraftUsername = *req.MinecraftUsername
	}

	// Username column is idempotent here — RenameUser already wrote it (plus
	// history row + last_username_change) in its own transaction.
	if err := h.state.Store.UpdateUser(user); err != nil {
		sendJSONError(w, "Update failed", 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"success": "true", "message": "Profile updated!"})
}

func (h *AuthHandler) StatusHandler(w http.ResponseWriter, r *http.Request) {
	// Setup is removed. If the API is reachable, the system is "Ready".
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"needsSetup": false, // Always false, frontend ignores setup
		"message":    "Ready",
	})
}
