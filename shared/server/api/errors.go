package api

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidInput  = errors.New("invalid input")
	ErrConflict      = errors.New("conflict")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrForbidden     = errors.New("forbidden")
)

// Error carries the message to show the user. Kind is one of the sentinels
// above, so errors.Is keeps classifying it while wrapping layers add log
// context that the user never sees.
type Error struct {
	Kind error
	Msg  string
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Kind }

func NotFound(format string, args ...any) error {
	return &Error{Kind: ErrNotFound, Msg: fmt.Sprintf(format, args...)}
}

func AlreadyExists(format string, args ...any) error {
	return &Error{Kind: ErrAlreadyExists, Msg: fmt.Sprintf(format, args...)}
}

func InvalidInput(format string, args ...any) error {
	return &Error{Kind: ErrInvalidInput, Msg: fmt.Sprintf(format, args...)}
}

func Conflict(format string, args ...any) error {
	return &Error{Kind: ErrConflict, Msg: fmt.Sprintf(format, args...)}
}

func Unauthorized(format string, args ...any) error {
	return &Error{Kind: ErrUnauthorized, Msg: fmt.Sprintf(format, args...)}
}

func Forbidden(format string, args ...any) error {
	return &Error{Kind: ErrForbidden, Msg: fmt.Sprintf(format, args...)}
}

// Error codes returned in ErrorResponse.Code.
const (
	CodeNotFound         = "not_found"
	CodeAlreadyExists    = "already_exists"
	CodeConflict         = "conflict"
	CodeInvalidInput     = "invalid_input"
	CodeValidationFailed = "validation_failed"
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeInternal         = "internal"
)

// ErrorResponse is the body of every non-2xx API response. Error is always a
// complete, human-readable message; Errors adds per-field detail for
// validation failures.
type ErrorResponse struct {
	Error     string            `json:"error"`
	Code      string            `json:"code"`
	Errors    []ValidationError `json:"errors,omitempty"`
	RequestID string            `json:"requestId,omitempty"`
}

// ValidationErrors aggregates multiple validation failures.
type ValidationErrors struct {
	Errors []ValidationError
}

type ValidationError struct {
	Component string `json:"component"`
	Field     string `json:"field"`
	Message   string `json:"message"`
}

type ValidationWarning struct {
	Component string `json:"component"`
	Message   string `json:"message"`
}

func (v *ValidationErrors) Error() string {
	msgs := make([]string, len(v.Errors))
	for i, e := range v.Errors {
		msgs[i] = fmt.Sprintf("%s.%s: %s", e.Component, e.Field, e.Message)
	}
	return "validation failed: " + strings.Join(msgs, "; ")
}

func (v *ValidationErrors) Add(component, field, message string) {
	v.Errors = append(v.Errors, ValidationError{
		Component: component,
		Field:     field,
		Message:   message,
	})
}

func (v *ValidationErrors) HasErrors() bool {
	return len(v.Errors) > 0
}
