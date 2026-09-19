package errors

import "errors"

// Domain-level sentinel errors. The transport layer maps these to HTTP status
// codes and error envelopes so the service layer stays transport-agnostic.
var (
	ErrInvalidInput = errors.New("invalid input")
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrConflict     = errors.New("conflict")
	ErrInternal     = errors.New("internal error")
)

// Is re-exports errors.Is so callers don't need to import the stdlib package
// alongside this one.
func Is(err, target error) bool {
	return errors.Is(err, target)
}
