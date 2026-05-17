package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/pondplatform/pond/shared/server/api"
)

// ListResponse wraps paginated list results.
type ListResponse[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// Pagination holds parsed pagination query parameters.
type Pagination struct {
	Limit  int
	Cursor string
}

const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// ParsePagination extracts limit and cursor from query params.
func ParsePagination(r *http.Request) Pagination {
	p := Pagination{
		Limit:  DefaultLimit,
		Cursor: r.URL.Query().Get("cursor"),
	}

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
			p.Limit = limit
		}
	}

	if p.Limit > MaxLimit {
		p.Limit = MaxLimit
	}

	return p
}

// writeJSON encodes v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeList writes a paginated list response.
func writeList[T any](w http.ResponseWriter, items []T, nextCursor *string) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, ListResponse[T]{
		Items:      items,
		NextCursor: nextCursor,
	})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeValidationError(w http.ResponseWriter, err *api.ValidationErrors) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	json.NewEncoder(w).Encode(map[string]any{
		"error":  "validation failed",
		"errors": err.Errors,
	})
}

func writeServiceError(w http.ResponseWriter, err error) {
	var ve *api.ValidationErrors
	switch {
	case errors.As(err, &ve):
		writeValidationError(w, ve)
	case errors.Is(err, api.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, api.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "already exists")
	case errors.Is(err, api.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, api.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid input")
	case errors.Is(err, api.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, api.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}
