package handlers

import (
	"dylaris-core/models"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

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

// createUserRequest is the wire-level payload — superset of models.User with
// optional region-access fields. Kept separate from models.User so we can
// add registration-specific fields in 0a.2+ without polluting the model.
type createUserRequest struct {
	models.User
	// Region access. If the caller omits both fields, the new
	// user defaults to all-regions access — matches the grandfather behavior
	// applied to existing users at migration time, and avoids creating users
	// who can see nothing.
	AllRegions    *bool    `json:"allRegions,omitempty"`
	RegionsExplicit []string `json:"regionsExplicit,omitempty"`
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

	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	if req.Password == "" {
		sendJSONError(w, "Password is required", 400)
		return
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("CreateUser: bcrypt hashing failed for username=%q: %v", req.Username, err)
		sendJSONError(w, "Could not create user", 500)
		return
	}
	req.Password = string(hashed)

	if err := h.state.Store.CreateUser(&req.User); err != nil {
		log.Printf("CreateUser failed for username=%q: %v", req.Username, err)
		sendJSONError(w, "Could not create user", 409)
		return
	}

	// Default to all-regions when caller hasn't specified — preserves
	// previous behavior where every user had implicit global access.
	allRegions := true
	regionsExplicit := []string{}
	if req.AllRegions != nil {
		allRegions = *req.AllRegions
	}
	if req.RegionsExplicit != nil {
		regionsExplicit = req.RegionsExplicit
	}
	if err := h.state.Store.SetUserRegions(req.User.ID, allRegions, regionsExplicit); err != nil {
		// Region setup failed but the user already exists — log but don't
		// roll back; admin can fix the assignment from the user settings panel.
		log.Printf("CreateUser: SetUserRegions failed for userID=%s: %v", req.User.ID, err)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "User created",
		"userId":  req.User.ID,
	})
}

// CancelUserDeletion POST /api/admin/users/{id}/cancel-deletion
// Admin override: clears the pending_deletion stamps and returns the user
// to active state. Useful when an admin wants to save an account before
// the user logs in themselves. Idempotent on already-active users.
func (h *UserHandler) CancelUserDeletion(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}
	if !IsAdmin(r) {
		sendJSONError(w, "Forbidden", 403)
		return
	}
	id, ok := parseUserID(w, r)
	if !ok {
		return
	}
	if err := h.state.Store.CancelUserDeletion(id); err != nil {
		sendJSONError(w, "Failed to cancel deletion", 500)
		return
	}
	actorID, _ := r.Context().Value("userID").(string)
	LogIdentityAudit(h.state, r, AuditEventDeletionCancelledByAdmin, actorID, id, nil)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
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

	id, ok := parseUserID(w, r)
	if !ok {
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

	id, ok := parseUserID(w, r)
	if !ok {
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
	id := vars["id"]
	scope := fmt.Sprintf("user:%s", id)

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
	id := vars["id"]
	scope := fmt.Sprintf("user:%s", id)

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
