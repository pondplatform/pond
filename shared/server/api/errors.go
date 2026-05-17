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

// ValidationErrors aggregates multiple validation failures.
type ValidationErrors struct {
	Errors []ValidationError
}

type ValidationError struct {
	Component string
	Field     string
	Message   string
}

type ValidationWarning struct {
	Component string
	Message   string
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
