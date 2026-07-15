package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
)

// adminCreateAllowed decides whether POST /api/setup/admin may create an admin.
// configured is AppState.AdminSecret (may be ""); provided is the request field.
//
//   - No secret configured: only a pristine fresh install (userCount == 0) is
//     open. With users present and no secret, /setup is closed (no recovery).
//   - Secret configured: creation is allowed in ANY mode iff the provided secret
//     matches, compared over SHA-256 digests so unequal input lengths cannot
//     leak via timing.
func adminCreateAllowed(userCount int, configured, provided string) bool {
	if configured == "" {
		return userCount == 0
	}
	p := sha256.Sum256([]byte(provided))
	c := sha256.Sum256([]byte(configured))
	return subtle.ConstantTimeCompare(p[:], c[:]) == 1
}
