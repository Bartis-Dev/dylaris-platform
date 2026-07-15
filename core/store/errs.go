package store

import "errors"

// Sentinel errors used by the setup wizard handler to map store
// outcomes to HTTP status codes without parsing error strings.
var (
	// ErrSetupAlreadyComplete is returned by CreateFirstAdmin when an admin
	// already exists. Handler maps it to 409 setup_already_complete.
	ErrSetupAlreadyComplete = errors.New("setup already complete")
)
