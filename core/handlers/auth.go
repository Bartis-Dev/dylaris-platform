package handlers

import (
	"context"
	"database/sql" // Import Models
	"encoding/json"
	"net/http"
	"strings"
	"time"

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

// ... Structs LoginRequest, Claims, UpdateRequest etc. kept as-is ...
type Claims struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
	jwt.RegisteredClaims
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
		// Fallback: EventSource (SSE) cannot send custom headers, so accept ?token= query param
		if authHeader == "" {
			if qToken := r.URL.Query().Get("token"); qToken != "" {
				authHeader = "Bearer " + qToken
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
		})
		if err != nil || !token.Valid {
			sendJSONError(w, "Invalid Token", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), "username", claims.Username)
		ctx = context.WithValue(ctx, "isAdmin", claims.IsAdmin)

		// Resolve userID from DB for invite checks
		if h.state.Store != nil {
			if user, err := h.state.Store.GetUserByUsername(claims.Username); err == nil {
				ctx = context.WithValue(ctx, "userID", user.ID)
			}
		}

		next(w, r.WithContext(ctx))
	}
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
		sendJSONError(w, "Database Error", http.StatusInternalServerError)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		sendJSONError(w, "Invalid Password", http.StatusUnauthorized)
		return
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

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"token":   tokenString,
		"isAdmin": user.IsAdmin,
	})
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

	// Update Fields in Struct
	if req.NewUsername != nil && *req.NewUsername != "" {
		user.Username = *req.NewUsername
	}
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

	// Save via Store
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
