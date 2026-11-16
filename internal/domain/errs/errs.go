package errs

import (
	"errors"
	"fmt"
)

// Kind identifies semantic domain error categories mapped by inbound adapters.
type Kind string

const (
	KindValidation   Kind = "validation"
	KindUnauthorized Kind = "unauthorized"
	KindForbidden    Kind = "forbidden"
	KindNotFound     Kind = "not_found"
	KindConflict     Kind = "conflict"
	KindLocked       Kind = "locked"
	KindRateLimit    Kind = "rate_limit"
	KindInternal     Kind = "internal"
)

// Error is a structured domain error carrying a safe client-facing message and field hints.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Fields  map[string]string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s (%s): %v", e.Message, e.Kind, e.Cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// Validation constructs a validation error with optional per-field messages.
func Validation(msg string, fields ...map[string]string) *Error {
	var f map[string]string
	if len(fields) > 0 {
		f = fields[0]
	}
	return &Error{Kind: KindValidation, Code: "validation_error", Message: msg, Fields: f}
}

func Unauthorized(msg string) *Error {
	return &Error{Kind: KindUnauthorized, Code: "unauthorized", Message: msg}
}

func Forbidden(msg string) *Error {
	return &Error{Kind: KindForbidden, Code: "forbidden", Message: msg}
}

func NotFound(msg string) *Error {
	return &Error{Kind: KindNotFound, Code: "not_found", Message: msg}
}

func Conflict(msg string) *Error {
	return &Error{Kind: KindConflict, Code: "conflict", Message: msg}
}

func Locked(msg string) *Error {
	return &Error{Kind: KindLocked, Code: "account_locked", Message: msg}
}

func RateLimit(msg string) *Error {
	return &Error{Kind: KindRateLimit, Code: "rate_limited", Message: msg}
}

func Internal(msg string, cause error) *Error {
	return &Error{Kind: KindInternal, Code: "internal_error", Message: msg, Cause: cause}
}

// KindOf extracts the domain error kind, defaulting to KindInternal.
func KindOf(err error) Kind {
	var de *Error
	if errors.As(err, &de) {
		return de.Kind
	}
	return KindInternal
}

// AsError unwraps err into *Error when present.
func AsError(err error) (*Error, bool) {
	var de *Error
	if errors.As(err, &de) {
		return de, true
	}
	return nil, false
}
