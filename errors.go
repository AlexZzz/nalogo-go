package nalogo

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrDomain is the root sentinel; all library errors wrap it.
var ErrDomain = errors.New("nalogo")

// HTTP-status sentinels — mirror upstream Python exception hierarchy 1:1.
var (
	ErrValidation       = fmt.Errorf("%w: validation (400)", ErrDomain)
	ErrUnauthorized     = fmt.Errorf("%w: unauthorized (401)", ErrDomain)
	ErrForbidden        = fmt.Errorf("%w: forbidden (403)", ErrDomain)
	ErrNotFound         = fmt.Errorf("%w: not found (404)", ErrDomain)
	ErrClient           = fmt.Errorf("%w: client error (406)", ErrDomain)
	ErrPhone            = fmt.Errorf("%w: phone error (422)", ErrDomain)
	ErrServer           = fmt.Errorf("%w: server error (500)", ErrDomain)
	ErrUnknown          = fmt.Errorf("%w: unknown error", ErrDomain)
	ErrNotAuthenticated = fmt.Errorf("%w: not authenticated", ErrDomain)
)

// APIError carries the HTTP status code and (masked) response body alongside
// the appropriate sentinel. It satisfies both errors.Is (via Is) and
// errors.As (via type assertion) for callers that need status details.
type APIError struct {
	Sentinel   error
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: status=%d body=%s", e.Sentinel, e.StatusCode, e.Body)
}

// Is reports true if target is e.Sentinel or ErrDomain.
func (e *APIError) Is(target error) bool {
	return errors.Is(e.Sentinel, target)
}

// Unwrap returns e.Sentinel so errors.As can walk the chain.
func (e *APIError) Unwrap() error {
	return e.Sentinel
}

func statusToSentinel(code int) error {
	switch code {
	case http.StatusBadRequest:
		return ErrValidation
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusNotAcceptable:
		return ErrClient
	case http.StatusUnprocessableEntity:
		return ErrPhone
	case http.StatusInternalServerError:
		return ErrServer
	default:
		return ErrUnknown
	}
}

// checkResponse returns an *APIError if resp.StatusCode >= 400, nil otherwise.
// The response body is read (up to 1000 bytes), sanitized, and attached to the error.
func checkResponse(resp *http.Response) error {
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
	return &APIError{
		Sentinel:   statusToSentinel(resp.StatusCode),
		StatusCode: resp.StatusCode,
		Body:       sanitizeBody(string(body)),
	}
}

// newValidationError creates a pre-flight *APIError without an HTTP response.
func newValidationError(msg string) error {
	return &APIError{
		Sentinel:   ErrValidation,
		StatusCode: http.StatusBadRequest,
		Body:       msg,
	}
}
