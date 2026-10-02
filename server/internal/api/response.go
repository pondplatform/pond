package api

import (
	"errors"
	"fmt"
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

// writeError writes an error envelope with a code derived from status.
func writeError(c *gin.Context, status int, msg string) {
	writeErrorBody(c, status, codeForStatus(status), msg, nil)
}

func writeErrorBody(c *gin.Context, status int, code, msg string, fieldErrs []api.ValidationError) {
	c.Set(errorMessageKey, msg)
	c.AbortWithStatusJSON(status, api.ErrorResponse{
		Error:     msg,
		Code:      code,
		Errors:    fieldErrs,
		RequestID: requestID(c),
	})
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return api.CodeInvalidInput
	case http.StatusUnauthorized:
		return api.CodeUnauthorized
	case http.StatusForbidden:
		return api.CodeForbidden
	case http.StatusNotFound:
		return api.CodeNotFound
	case http.StatusMethodNotAllowed:
		return api.CodeMethodNotAllowed
	case http.StatusConflict:
		return api.CodeConflict
	case http.StatusUnprocessableEntity:
		return api.CodeValidationFailed
	default:
		return api.CodeInternal
	}
}

func writeValidationError(c *gin.Context, err *api.ValidationErrors) {
	writeErrorBody(c, http.StatusUnprocessableEntity, api.CodeValidationFailed, err.Error(), err.Errors)
}

// writeInternalError hides err from the client but logs it under the request
// ID the client is given, so a reported failure can be found in the logs.
func writeInternalError(c *gin.Context, err error, log *slog.Logger) {
	id := requestID(c)
	log.Error("internal server error",
		"err", err,
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"request_id", id,
	)
	writeErrorBody(c, http.StatusInternalServerError, api.CodeInternal,
		fmt.Sprintf("internal server error (request ID %s); the server log has the details", id), nil)
}

var errorKinds = []struct {
	kind   error
	status int
	code   string
}{
	{api.ErrNotFound, http.StatusNotFound, api.CodeNotFound},
	{api.ErrAlreadyExists, http.StatusConflict, api.CodeAlreadyExists},
	{api.ErrConflict, http.StatusConflict, api.CodeConflict},
	{api.ErrInvalidInput, http.StatusBadRequest, api.CodeInvalidInput},
	{api.ErrUnauthorized, http.StatusUnauthorized, api.CodeUnauthorized},
	{api.ErrForbidden, http.StatusForbidden, api.CodeForbidden},
}

func writeServiceError(c *gin.Context, err error, log *slog.Logger) {
	var ve *api.ValidationErrors
	if errors.As(err, &ve) {
		writeValidationError(c, ve)
		return
	}
	for _, k := range errorKinds {
		if errors.Is(err, k.kind) {
			writeErrorBody(c, k.status, k.code, userMessage(err), nil)
			return
		}
	}
	writeInternalError(c, err, log)
}

// userMessage prefers the message written for the user at the point of
// failure over the wrap chain, which is phrased for logs.
func userMessage(err error) string {
	var ue *api.Error
	if errors.As(err, &ue) {
		return ue.Msg
	}
	return err.Error()
}
