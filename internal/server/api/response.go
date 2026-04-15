package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// ListResponse wraps paginated list results.
type ListResponse[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
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
