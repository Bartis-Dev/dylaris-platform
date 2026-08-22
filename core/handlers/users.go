package handlers

import (
	"dylaris-core/models"
	"dylaris-core/store"
	"dylaris-pkg/validate"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

type UserHandler struct {
	state *AppState
}

func NewUserHandler(state *AppState) *UserHandler {
	return &UserHandler{state: state}
}

// GetAllUsers GET /api/users - every account.
func (h *UserHandler) GetAllUsers(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
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
	// Password shadows models.User.Password, which is json:"-" so that a user
	// row can never be serialised back to a client with its bcrypt hash on it.
	// That tag also applies on the way IN, which silently made this the one
	// field an admin could not send: every create arrived with an empty
	// password and was refused with "Password is required". Declaring it here
	// keeps the hash off every response and still lets the create payload
	// carry one. Plaintext on the way in, hashed into req.User.Password below
	// - never assign this field to the model directly.
	Password string `json:"password"`
	// Region access. If the caller omits both fields, the new
	// user defaults to all-regions access — matches the grandfather behavior
	// applied to existing users at migration time, and avoids creating users
	// who can see nothing.
	AllRegions      *bool    `json:"allRegions,omitempty"`
	RegionsExplicit []string `json:"regionsExplicit,omitempty"`
}

// CreateUser POST /api/users - creates an account and assigns its regions. A
// region assignment that fails is logged but does not fail the creation, so
// the account still exists afterwards.
func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
		return
	}

	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", 400)
		return
	}

	// The admin create-user path historically validated neither the username nor
	// the password. A username is interpolated into Redis keys (the beam daily
	// counter dylaris:beam:daily:<username>), so a ':'/space must be rejected here
	// exactly like the register + rename paths.
	req.Username = strings.TrimSpace(req.Username)
	if !validate.IsUsername(req.Username) {
		sendJSONError(w, "Invalid username: 3-32 characters, must start with a letter or digit, then letters, digits, '.', '_' or '-'", 400)
		return
	}
	if req.Password == "" {
		sendJSONError(w, "Password is required", 400)
		return
	}
	// Enforce the same password-length policy the register + reset paths apply.
	if min := LoadAuthPolicy(h.state).PasswordMinLength; len(req.Password) < min {
		sendJSONError(w, fmt.Sprintf("Password must be at least %d characters", min), 400)
		return
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("CreateUser: bcrypt hashing failed for username=%q: %v", req.Username, err)
		sendJSONError(w, "Could not create user", 500)
		return
	}
	// req.User, not req: CreateUser persists the embedded model, and the
	// shadowing field above is only the wire's plaintext.
	req.User.Password = string(hashed)

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

// otherAdminExists reports whether any admin other than excludeID exists. The
// error is kept rather than collapsed: "we could not count them" and "there are
// none" call for opposite decisions at the one place this is asked.
func (h *UserHandler) otherAdminExists(excludeID string) (bool, error) {
	users, err := h.state.Store.ListUsers()
	if err != nil {
		return false, err
	}
	for _, u := range users {
		if u.IsAdmin && u.ID != excludeID {
			return true, nil
		}
	}
	return false, nil
}

// DeleteUser DELETE /api/users/{id} - deletes an account. A user still
// referenced elsewhere, for instance by server invites they issued, answers
// 409 with that reason rather than a bare 500.
func (h *UserHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if h.state.Store == nil {
		sendJSONError(w, "Database not connected", 503)
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

	// Never let the platform reach zero admins. "You cannot delete yourself"
	// does not cover it: users.delete is a delegatable capability, so a
	// non-admin holding it could remove every admin and lock the owner out of
	// their own panel with no way back in short of the database.
	if err == nil && userToDelete != nil && userToDelete.IsAdmin {
		others, cerr := h.otherAdminExists(userToDelete.ID)
		if cerr != nil {
			// Refuse when the answer is unknown. Guessing "yes" here is the one
			// direction that is unrecoverable.
			sendJSONError(w, "Could not verify how many admins remain", 503)
			return
		}
		if !others {
			sendJSONError(w, "This is the last admin. Make someone else an admin first.", 409)
			return
		}
	}

	if err := h.state.Store.DeleteUser(id); err != nil {
		// These two are not faults, they are the current state of the data, so
		// they answer 409 and say what to do about it. Postgres already produces
		// a precise reason ("still referenced from table servers"); collapsing it
		// into "Delete failed" left an admin with a button that does nothing and
		// no way to find out why.
		switch {
		case errors.Is(err, store.ErrUserOwnsServers):
			sendJSONError(w, "This user still owns servers. Transfer or delete their servers first.", 409)
		case errors.Is(err, store.ErrUserStillReferenced):
			sendJSONError(w, "This user is still referenced by other records (for example server invites they issued) and cannot be deleted yet.", 409)
		default:
			sendJSONError(w, "Delete failed", 500)
		}
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ResetUserPassword PUT /api/users/{id}/password - sets a new password for an
// account, hashed before it is stored. No current password is asked for; this
// is the admin path, not the self-service one.
func (h *UserHandler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
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
	if min := LoadAuthPolicy(h.state).PasswordMinLength; len(req.Password) < min {
		sendJSONError(w, fmt.Sprintf("Password must be at least %d characters", min), 400)
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

// GetUserRouteLimit GET /api/users/{id}/route-limit - one user's gateway route
// allowance: default when no override exists, otherwise custom or disabled.
func (h *UserHandler) GetUserRouteLimit(w http.ResponseWriter, r *http.Request) {
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

// SetUserRouteLimit PUT /api/users/{id}/route-limit - sets a user's gateway
// route allowance to default, custom with a count, or disabled.
func (h *UserHandler) SetUserRouteLimit(w http.ResponseWriter, r *http.Request) {
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
