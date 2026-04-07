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
}
type UpdateRequest struct {
	OldPassword       string  `json:"oldPassword"`
	NewUsername       *string `json:"newUsername,omitempty"`
	NewPassword       *string `json:"newPassword,omitempty"`
	MinecraftUsername *string `json:"minecraftUsername,omitempty"`
	Email             *string `json:"email,omitempty"`
	Is2FAEnabled      *bool   `json:"is2FAEnabled,omitempty"`
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
	if req.Is2FAEnabled != nil {
		user.Is2FAEnabled = *req.Is2FAEnabled
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
