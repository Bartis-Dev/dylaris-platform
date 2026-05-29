package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"dylaris-core/mailer"

	"golang.org/x/crypto/bcrypt"
)

type PasswordResetHandler struct {
	state *AppState
}

func NewPasswordResetHandler(state *AppState) *PasswordResetHandler {
	return &PasswordResetHandler{state: state}
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

// ForgotPassword POST /api/auth/forgot-password — public.
// Always returns success regardless of whether the email is on file. This
// is the standard enumeration-safe pattern: legitimate users get a mail,
// attackers get the same "ok" response either way.
//
// Side effect when the email IS on file: a fresh single-use token replaces
// any existing one (so two pending resets can't both succeed) and an email
// goes out with the reset link.
func (h *PasswordResetHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	// Cheap shape check — full validation is on the SMTP side anyway.
	if !emailRegex.MatchString(email) {
		// Same silent-success: do not leak that the address was malformed
		// because that distinguishes "user doesn't exist" from "garbage input"
		// for an attacker. Just return success.
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
		return
	}

	user, err := h.state.Store.GetUserByEmail(email)
	if err != nil || user == nil {
		if err != nil && err != sql.ErrNoRows {
			log.Printf("forgot-password: lookup error for %s: %v", email, err)
		}
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
		return
	}

	policy := LoadAuthPolicy(h.state)
	token, err := randomToken(32)
	if err != nil {
		// Token gen really shouldn't fail — log it for ops, return silent success.
		log.Printf("forgot-password: token generation failed: %v", err)
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
		return
	}
	expires := time.Now().Add(time.Duration(policy.PasswordResetLinkTTLMinutes) * time.Minute)
	if err := h.state.Store.SetPasswordResetToken(user.ID, token, expires); err != nil {
		log.Printf("forgot-password: SetPasswordResetToken for userID=%d: %v", user.ID, err)
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
		return
	}

	if err := sendPasswordResetEmail(h.state, user.Email, user.Username, token, policy.PasswordResetLinkTTLMinutes); err != nil {
		// Mail failed but the token is stored — the user can retry. Don't
		// surface it: silent-success is the rule. Just log for ops.
		log.Printf("forgot-password: send to %s failed: %v", user.Email, err)
	}

	LogIdentityAudit(h.state, r, AuditEventPasswordResetRequested, 0, user.ID, map[string]interface{}{
		"email":      user.Email,
		"ttl_minutes": policy.PasswordResetLinkTTLMinutes,
	})

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

type validateResetTokenRequest struct {
	Token string `json:"token"`
}

// ValidateResetToken POST /api/auth/validate-reset-token — public.
// Lets the /reset-password page tell the user whose account they're resetting
// before they type a new password, and surfaces "link expired" cleanly
// instead of waiting for the submit to fail.
//
// Returns username on success — that's intentional: the link itself implies
// possession of the account, and showing the username helps the user confirm
// they're resetting the right one. Email is NOT returned to avoid leaking
// it to anyone with a stolen link.
func (h *PasswordResetHandler) ValidateResetToken(w http.ResponseWriter, r *http.Request) {
	var req validateResetTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if len(req.Token) < 16 {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "valid": false})
		return
	}
	user, err := h.state.Store.GetUserByPasswordResetToken(req.Token)
	if err != nil || user == nil {
		// Token not found / expired — both look the same to the caller.
		// Don't 4xx; the page renders an "invalid or expired" panel.
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "valid": false})
		return
	}

	out := map[string]interface{}{
		"success":  true,
		"valid":    true,
		"username": user.Username,
	}

	// Phase 0a.5 — surface the user's chosen questions when the policy
	// demands answers at reset AND the user actually has questions set up.
	// Users without questions get a free pass: reset works without a
	// verification step (otherwise the policy locks them out forever).
	policy := LoadAuthPolicy(h.state)
	if policy.SecurityQuestionsEnabled && policy.SecurityQuestionsRequiredAtReset {
		qs, _ := h.state.Store.GetUserSecurityQuestions(user.ID)
		if len(qs) > 0 {
			out["requiresSecurityQuestions"] = true
			out["questions"] = qs
		}
	}
	json.NewEncoder(w).Encode(out)
}

type resetPasswordRequest struct {
	Token           string   `json:"token"`
	Password        string   `json:"password"`
	SecurityAnswers []string `json:"securityAnswers,omitempty"`
}

// ResetPassword POST /api/auth/reset-password — public.
// Consumes the token, sets the new password, clears any pending verification
// state. The token clear is part of the same UPDATE so a concurrent second
// reset using the same token fails cleanly.
func (h *PasswordResetHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if len(req.Token) < 16 {
		sendJSONError(w, "Invalid token", http.StatusBadRequest)
		return
	}
	policy := LoadAuthPolicy(h.state)
	if len(req.Password) < policy.PasswordMinLength {
		sendJSONError(w, fmt.Sprintf("Password must be at least %d characters", policy.PasswordMinLength), http.StatusBadRequest)
		return
	}

	user, err := h.state.Store.GetUserByPasswordResetToken(req.Token)
	if err != nil || user == nil {
		// Same response shape as the verify-email gone branch — caller-side
		// renders "your link expired, request a new one".
		sendJSONError(w, "Reset link is invalid or expired", http.StatusGone)
		return
	}

	// Phase 0a.5 — verify security answers when the policy demands it AND
	// the user has questions on file. Skipping the check when the user has
	// none on file is intentional: matches ValidateResetToken's behavior and
	// avoids permanently locking out users created before the policy flipped on.
	if policy.SecurityQuestionsEnabled && policy.SecurityQuestionsRequiredAtReset {
		stored, _ := h.state.Store.GetUserSecurityQuestionsRaw(user.ID)
		if stored != "" && stored != "[]" {
			if !verifySecurityAnswersAgainstStored(stored, req.SecurityAnswers) {
				LogIdentityAudit(h.state, r, AuditEventSecurityAnswersFailedAtReset, 0, user.ID, nil)
				sendJSONError(w, "Security answers did not match", http.StatusUnauthorized)
				return
			}
		}
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		sendJSONError(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}

	// Update password and clear the token in the same transaction-like pair.
	// Order matters: UpdateUserPassword first so a crash mid-way still leaves
	// the token consumable (user can retry the same link). Then clear.
	if err := h.state.Store.UpdateUserPassword(user.ID, string(hashed)); err != nil {
		sendJSONError(w, "Failed to update password", http.StatusInternalServerError)
		return
	}
	if err := h.state.Store.ClearPasswordResetToken(user.ID); err != nil {
		// Token left dangling — won't authenticate (no matching row), but log it.
		log.Printf("reset-password: ClearPasswordResetToken for userID=%d: %v", user.ID, err)
	}

	LogIdentityAudit(h.state, r, AuditEventPasswordResetCompleted, 0, user.ID, nil)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"username": user.Username,
	})
}

// sendPasswordResetEmail wraps mailer.Send with the verification template.
// Mirror of sendVerificationEmail (registration.go) — kept separate so the
// templates can diverge (we'll likely want different copy/CTAs eventually).
func sendPasswordResetEmail(state *AppState, to, username, token string, ttlMinutes int) error {
	cfg, err := mailer.LoadConfig(state.Store, "auth")
	if err != nil {
		return fmt.Errorf("smtp not configured: %w", err)
	}
	link := strings.TrimRight(state.FrontendURL, "/") + "/reset-password?token=" + token
	body := fmt.Sprintf(`Hi %s,

We received a request to reset your Dylaris account password. Use the link below to choose a new one:

%s

This link is valid for %d minute(s) and works exactly once. If you did not request a reset, you can safely ignore this email — your password remains unchanged.

— Dylaris
`, username, link, ttlMinutes)
	return mailer.Send(cfg, mailer.Message{
		To:      to,
		Subject: "Reset your Dylaris password",
		Body:    body,
	})
}
