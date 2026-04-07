package handlers

import (
	"dylaris-core/models"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

type UserHandler struct {
	state *AppState
}

func NewUserHandler(state *AppState) *UserHandler {
	return &UserHandler{state: state}
}

func (h *UserHandler) GetAllUsers(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Access denied", 403)
		return
	}

	users, err := h.state.Store.ListUsers()
	if err != nil {
		sendJSONError(w, "Failed to fetch users", 500)
		return
	}

	for i := range users {
		users[i].Password = ""
	}

	if users == nil {
		users = []models.User{}
	}

	// FIX: Return as object
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"users":   users,
	})
}

func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	var req models.User
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	hashed, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	req.Password = string(hashed)

	if err := h.state.Store.CreateUser(&req); err != nil {
		sendJSONError(w, "Could not create user", 409)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "User created"})
}

func (h *UserHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid ID", 400)
		return
	}

	currentUser := r.Context().Value("username").(string)
	userToDelete, err := h.state.Store.GetUserByID(id)

	if err == nil && userToDelete.Username == currentUser {
		sendJSONError(w, "You cannot delete yourself", 403)
		return
	}

	if err := h.state.Store.DeleteUser(id); err != nil {
		sendJSONError(w, "Delete failed", 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ResetUserPassword PUT /api/users/{id}/password
func (h *UserHandler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		sendJSONError(w, "Invalid ID", 400)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		sendJSONError(w, "Password is required", 400)
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		sendJSONError(w, "Failed to hash password", 500)
		return
	}

	if err := h.state.Store.UpdateUserPassword(id, string(hashed)); err != nil {
		sendJSONError(w, "Failed to update password", 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Password updated"})
}

// GetUserRouteLimit GET /api/users/{id}/route-limit
func (h *UserHandler) GetUserRouteLimit(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])
	scope := fmt.Sprintf("user:%d", id)

	limit, err := h.state.Store.GetGatewayRouteLimit(scope)
	if err != nil {
		// No override → default mode
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"mode":      "default",
			"maxRoutes": 0,
		})
		return
	}

	mode := "custom"
	if limit.MaxRoutes == 0 {
		mode = "disabled"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"mode":      mode,
		"maxRoutes": limit.MaxRoutes,
	})
}

// SetUserRouteLimit PUT /api/users/{id}/route-limit
func (h *UserHandler) SetUserRouteLimit(w http.ResponseWriter, r *http.Request) {
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}

	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])
	scope := fmt.Sprintf("user:%d", id)

	var req struct {
		Mode      string `json:"mode"`
		MaxRoutes int    `json:"maxRoutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	switch req.Mode {
	case "default":
		// Remove user-specific override → falls back to user_default
		h.state.Store.DeleteGatewayRouteLimit(scope)
	case "custom":
		if req.MaxRoutes < 1 {
			sendJSONError(w, "Custom limit must be at least 1", 400)
			return
		}
		h.state.Store.SetGatewayRouteLimit(scope, req.MaxRoutes)
	case "disabled":
		h.state.Store.SetGatewayRouteLimit(scope, 0)
	default:
		sendJSONError(w, "Invalid mode (use: default, custom, disabled)", 400)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Route limit updated"})
}
