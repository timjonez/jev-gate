package herdrx

import (
	"errors"
	"fmt"
)

var (
	// ErrUnavailable means the Herdr socket could not be reached.
	ErrUnavailable = errors.New("herdr unavailable")
	// ErrInvalid is a local argument error.
	ErrInvalid = errors.New("invalid")
	// ErrNotFound is a Herdr not_found response.
	ErrNotFound = errors.New("not found")
)

// APIError is a Herdr error response.
type APIError struct {
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("herdr: %s", e.Code)
	}
	if e.Code == "" {
		return "herdr: " + e.Message
	}
	return fmt.Sprintf("herdr: %s: %s", e.Code, e.Message)
}

func (e *APIError) Unwrap() error {
	if e != nil && e.Code == "not_found" {
		return ErrNotFound
	}
	return nil
}

// IsAgentNotReady reports Herdr agent_not_ready (unnamed or not yet promptable).
func IsAgentNotReady(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Code == "agent_not_ready"
}

// IsPaneBusy reports Herdr agent_pane_busy (new pane not yet an available shell).
func IsPaneBusy(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Code == "agent_pane_busy"
}
