package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
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

func writeJSON(c *gin.Context, status int, v any) {
	c.JSON(status, v)
}

func writeList[T any](c *gin.Context, items []T, nextCursor *string) {
	if items == nil {
		items = []T{}
	}
	c.JSON(http.StatusOK, ListResponse[T]{
		Items:      items,
		NextCursor: nextCursor,
	})
}

func writeError(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"error": msg})
}

func writeValidationError(c *gin.Context, err *api.ValidationErrors) {
	c.JSON(http.StatusUnprocessableEntity, gin.H{
		"error":  "validation failed",
		"errors": err.Errors,
	})
}

func writeServiceError(c *gin.Context, err error, log *slog.Logger) {
	var ve *api.ValidationErrors
	switch {
	case errors.As(err, &ve):
		writeValidationError(c, ve)
	case errors.Is(err, api.ErrNotFound):
		writeError(c, http.StatusNotFound, "not found")
	case errors.Is(err, api.ErrAlreadyExists):
		writeError(c, http.StatusConflict, "already exists")
	case errors.Is(err, api.ErrConflict):
		writeError(c, http.StatusConflict, "conflict")
	case errors.Is(err, api.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, "invalid input")
	case errors.Is(err, api.ErrUnauthorized):
		writeError(c, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, api.ErrForbidden):
		writeError(c, http.StatusForbidden, "forbidden")
	default:
		log.Error("internal server error", "err", err)
		writeError(c, http.StatusInternalServerError, "internal server error")
	}
}
