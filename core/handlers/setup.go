package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"dylaris-core/models"
	"dylaris-core/store"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// SetupHandler covers /api/setup/*. Two open routes:
//
//	GET  /api/setup/status — what mode are we in?
//	POST /api/setup/admin  — create the first admin
//
// Mode is inferred per-request from the DB:
//   - fresh_install: user_count = 0 (no users at all)
//   - lost_admin:    admin_count = 0 AND user_count >= 1
//   - complete:      admin_count >= 1
type SetupHandler struct {
	state *AppState
	auth  *AuthHandler // for JWT issuance after successful setup
}

func NewSetupHandler(state *AppState, auth *AuthHandler) *SetupHandler {
	return &SetupHandler{state: state, auth: auth}
}

var setupUsernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,32}$`)

type setupStatusResp struct {
	Success               bool   `json:"success"`
	Mode                  string `json:"mode"`
	AdminSecretConfigured bool   `json:"adminSecretConfigured"`
	FrontendURL           string `json:"frontendUrl,omitempty"`
}

// Status GET /api/setup/status - open route. Always reachable (the setup-lock
// middleware passes /api/setup/* through unchanged). adminSecretConfigured lets
// the panel pick its wizard UI without exposing the secret itself.
func (h *SetupHandler) Status(w http.ResponseWriter, r *http.Request) {
	adminCount, _ := h.state.Store.CountAdmins()
	userCount, _ := h.state.Store.CountUsers()
	out := setupStatusResp{
		Success:               true,
		FrontendURL:           h.state.FrontendURL,
		AdminSecretConfigured: h.state.AdminSecretConfigured(),
	}
	switch {
	case adminCount > 0:
		out.Mode = "complete"
	case userCount == 0:
		out.Mode = "fresh_install"
	default:
		out.Mode = "lost_admin"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type setupTOTPInfo struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

type setupAdminReq struct {
	Username    string         `json:"username"`
	Password    string         `json:"password"`
	AdminSecret string         `json:"adminSecret,omitempty"`
	TOTP        *setupTOTPInfo `json:"totp,omitempty"`
}

type setupAdminResp struct {
	Success bool        `json:"success"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
	User    interface{} `json:"user,omitempty"`
	Token   string      `json:"token,omitempty"`
}

// CreateAdmin POST /api/setup/admin - open route (rate-limited). adminCreateAllowed
// enforces the ADMIN_SECRET rule BEFORE any username/password work so an
// unauthorized caller learns nothing about validation. The guarded CTE in
// CreateFirstAdmin still protects against racing first-admin inserts across
// N Cores; CreateAdditionalAdmin serves the break-glass path.
func (h *SetupHandler) CreateAdmin(w http.ResponseWriter, r *http.Request) {
	var req setupAdminReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendSetupError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON.")
		return
	}

	userCount, _ := h.state.Store.CountUsers()
	adminCount, _ := h.state.Store.CountAdmins()

	if !adminCreateAllowed(userCount, h.state.AdminSecret, req.AdminSecret) {
		if h.state.AdminSecretConfigured() {
			sendSetupError(w, http.StatusForbidden, "invalid_admin_secret", "The admin secret is missing or incorrect.")
		} else {
			sendSetupError(w, http.StatusForbidden, "admin_recovery_disabled", "Admin creation is closed. Set ADMIN_SECRET in Core's environment and restart to create a new admin.")
		}
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if !setupUsernameRegex.MatchString(req.Username) {
		sendSetupError(w, http.StatusBadRequest, "invalid_username", "Username must be 3-32 chars (alphanumeric, _ or -).")
		return
	}
	if len(req.Password) < 8 {
		sendSetupError(w, http.StatusBadRequest, "invalid_password", "Password must be at least 8 characters.")
		return
	}
	if req.TOTP != nil {
		if req.TOTP.Secret == "" || req.TOTP.Code == "" {
			sendSetupError(w, http.StatusBadRequest, "invalid_totp", "TOTP secret + code required.")
			return
		}
		if !totp.Validate(req.TOTP.Code, req.TOTP.Secret) {
			sendSetupError(w, http.StatusBadRequest, "invalid_totp", "TOTP code did not verify.")
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		sendSetupError(w, http.StatusInternalServerError, "hash_failed", "Could not hash password.")
		return
	}
	totpSecret := ""
	if req.TOTP != nil {
		totpSecret = req.TOTP.Secret
	}

	var user *models.User
	if adminCount == 0 {
		// Guarded CTE: race-safe first admin (Fresh-Install or Lost-Admin).
		user, err = h.state.Store.CreateFirstAdmin(req.Username, string(hash), totpSecret)
	} else {
		// Only reachable with a matching secret (break-glass additional admin).
		user, err = h.state.Store.CreateAdditionalAdmin(req.Username, string(hash), totpSecret)
	}
	if err != nil {
		switch {
		case errors.Is(err, store.ErrSetupAlreadyComplete):
			sendSetupError(w, http.StatusConflict, "setup_already_complete", "Setup is already complete. Go to /login.")
		case errors.Is(err, store.ErrUsernameTaken):
			sendSetupError(w, http.StatusConflict, "username_taken", "That username is already taken. Choose another.")
		default:
			sendSetupError(w, http.StatusInternalServerError, "create_failed", err.Error())
		}
		return
	}

	token, err := h.auth.IssueToken(user.Username, user.IsAdmin)
	if err != nil {
		sendSetupError(w, http.StatusInternalServerError, "token_failed", "Admin created but token issuance failed: "+err.Error())
		return
	}

	// SSE so other Cores' setup-lock middleware unlocks instantly + panels
	// re-check setup status.
	if h.state.Events != nil {
		h.state.Events.Publish(r.Context(), "setup.completed", nil)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(setupAdminResp{
		Success: true, User: user, Token: token,
	})
}

func sendSetupError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(setupAdminResp{Success: false, Error: code, Message: msg})
}
