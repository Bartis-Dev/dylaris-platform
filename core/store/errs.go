package store

import (
	"errors"

	"github.com/lib/pq"
)

// Sentinel errors used by the setup wizard handler to map store
// outcomes to HTTP status codes without parsing error strings.
var (
	// ErrSetupAlreadyComplete is returned by CreateFirstAdmin when an admin
	// already exists. Handler maps it to 409 setup_already_complete.
	ErrSetupAlreadyComplete = errors.New("setup already complete")

	// ErrNameTaken is returned by a create or rename that hit a UNIQUE(name)
	// violation. Handlers map it to 409. It exists so they never have to echo
	// the driver's message, which spells out the table and the constraint.
	ErrNameTaken = errors.New("name already in use")
)

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505). Kept unexported: callers outside the store see
// ErrNameTaken, not the driver's error class.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}
