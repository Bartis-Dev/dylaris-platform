package handlers

import (
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// Re-authentication for the actions that turn a borrowed session into a
// permanent one.
//
// Session invalidation in this codebase is strong and covers every path:
// AuthMiddleware compares a fingerprint derived from the stored password hash,
// so a self-service change, an admin reset, an emailed reset and any write path
// added later all kill every outstanding session at once, with nobody having to
// remember to stamp a column.
//
// It reaches neither of these two:
//
//   - an API key authenticates by its OWN hash (GetAPIKeyByHash), so it keeps
//     working after the password change that killed every session;
//   - security questions ARE the password-reset path, so overwriting them
//     survives even revoking every key.
//
// So both are asked for the credential again, the way DisableTOTP and
// RegenerateBackupCodes already were. Same two fields, so the API has one
// convention rather than a second one invented here.
type reauthFields struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// reauthError carries the status the caller has to answer with. A wrong
// password and a broken database are not the same event and must not collapse
// into one message.
type reauthError struct {
	status  int
	message string
}

func (e *reauthError) Error() string { return e.message }

// requireReauth re-proves the identity behind userID. nil means proceed.
//
// The password alone is enough for an account with no 2FA, and that carve-out
// is deliberate rather than an oversight: there is no second factor to ask such
// an account for, and demanding a code anyway would not harden anything - it
// would lock every user without 2FA out of the action completely. The password
// is the entire credential at login for those accounts too.
//
// Backup codes count, via verifyTOTPOrBackupFor. These are account-management
// actions, and a user whose phone is gone has to be able to manage their
// account; the ticket-restore Danger Zone deliberately refuses them for the
// opposite reason, being destructive rather than administrative.
func requireReauth(state *AppState, userID, password, code string) *reauthError {
	if state == nil || state.Store == nil {
		return &reauthError{http.StatusServiceUnavailable, "Database not connected"}
	}
	user, err := state.Store.GetUserByID(userID)
	if err != nil || user == nil {
		return &reauthError{http.StatusUnauthorized, "Unauthorized"}
	}
	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)) != nil {
		return &reauthError{http.StatusUnauthorized, "Your password is not correct"}
	}
	if !user.Is2FAEnabled {
		return nil
	}
	ok, verr := verifyTOTPOrBackupFor(state, user, code)
	if verr != nil {
		return &reauthError{http.StatusInternalServerError, "Verification failed"}
	}
	if !ok {
		return &reauthError{http.StatusUnauthorized, "Your two-factor code is not correct"}
	}
	return nil
}

// writeReauthError answers a failed re-authentication.
func writeReauthError(w http.ResponseWriter, e *reauthError) {
	sendJSONError(w, e.message, e.status)
}
