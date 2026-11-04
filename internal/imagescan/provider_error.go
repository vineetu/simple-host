package imagescan

import (
	"context"
	"errors"
	"fmt"
)

// ProviderError carries safe metadata alongside the existing provider error.
// Call SafeError at status/log boundaries; direct callers retain error details.
type ProviderError struct {
	Operation  string
	HTTPStatus int
	Cause      string
	err        error
}

func (e *ProviderError) Error() string { return e.err.Error() }

type safeProviderError struct{ *ProviderError }

func (e *safeProviderError) Unwrap() error { return e.ProviderError }

func (e *safeProviderError) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("%s: HTTP %d: %s", e.Operation, e.HTTPStatus, e.Cause)
	}
	return e.Operation + ": " + e.Cause
}
func (e *ProviderError) Unwrap() error { return e.err }

// SafeError maps even third-party Matcher/Reporter errors to fixed safe causes.
func SafeError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var provider *ProviderError
	if errors.As(err, &provider) {
		return &safeProviderError{provider}
	}
	return &safeProviderError{providerError(operation, 0, err)}
}
func providerError(operation string, status int, err error) *ProviderError {
	cause := "provider rejected request or returned an invalid response"
	switch {
	case errors.Is(err, context.Canceled):
		cause = "request canceled"
	case errors.Is(err, context.DeadlineExceeded):
		cause = "request timed out"
	case status == 401 || status == 403:
		cause = "subscription key refused"
	case errors.Is(err, ErrUnsupported):
		cause = "unsupported image"
	case errors.Is(err, ErrRetryable):
		cause = "temporary provider failure"
	}
	return &ProviderError{Operation: operation, HTTPStatus: status, Cause: cause, err: err}
}
