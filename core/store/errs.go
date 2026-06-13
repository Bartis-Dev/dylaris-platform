package store

import "errors"

// Sentinel errors used by the setup wizard handler to map store
// outcomes to HTTP status codes without parsing error strings.
var (
	// ErrSetupAlreadyComplete is returned by CreateFirstAdmin when an admin
	// already exists. Handler maps it to 409 setup_already_complete.
	ErrSetupAlreadyComplete = errors.New("setup already complete")

	// ErrSetupInvalidToken is returned by CreateFirstAdmin in Lost-Admin
	// mode when the supplied recovery token doesn't match the stored one.
	// Handler maps it to 403 invalid_recovery_token.
	ErrSetupInvalidToken = errors.New("invalid recovery token")
)
