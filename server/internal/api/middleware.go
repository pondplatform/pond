package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/shared/server/api"
)

const (
	requestIDKey    = "request_id"
	errorMessageKey = "error_message"
	requestIDHeader = "X-Request-ID"
)

// GinRequestID tags each request with an ID, returned in the X-Request-ID
// header and in error bodies, so users can quote it and operators can find
// the matching log lines.
func GinRequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uuid.NewString()
		c.Set(requestIDKey, id)
		c.Header(requestIDHeader, id)
		c.Next()
	}
}

func requestID(c *gin.Context) string {
	return c.GetString(requestIDKey)
}

// GinLogger is a gin middleware that logs each request using slog. Error
// responses include the message the client was sent.
func GinLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", requestID(c),
		}
		if msg := c.GetString(errorMessageKey); msg != "" {
			attrs = append(attrs, "error", msg)
		}
		log.Info("request", attrs...)
	}
}

// GinRecovery turns a panic into a JSON 500 that carries the request ID.
func GinRecovery(log *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		err := fmt.Errorf("panic: %v", recovered)
		if c.Writer.Written() {
			log.Error("panic after response was written", "err", err, "request_id", requestID(c))
			c.Abort()
			return
		}
		writeInternalError(c, err, log)
	})
}

func handleNoRoute(c *gin.Context) {
	writeError(c, http.StatusNotFound, fmt.Sprintf("no API endpoint %s %s", c.Request.Method, c.Request.URL.Path))
}

func handleNoMethod(c *gin.Context) {
	writeError(c, http.StatusMethodNotAllowed, fmt.Sprintf("method %s is not supported for %s", c.Request.Method, c.Request.URL.Path))
}

// GinRequireAuth validates the bearer token and injects *domain.Identity into the request context.
// Aborts with 401 if the token is missing or invalid.
func GinRequireAuth(authenticator auth.Authenticator, log *slog.Logger) gin.HandlerFunc {
	if log == nil {
		log = slog.Default()
	}
	return func(c *gin.Context) {
		identity, err := authenticator.Authenticate(c.Request.Context(), c.Request)
		if err != nil {
			if errors.Is(err, api.ErrUnauthorized) {
				writeErrorBody(c, http.StatusUnauthorized, api.CodeUnauthorized, userMessage(err), nil)
				return
			}
			writeInternalError(c, fmt.Errorf("authenticate: %w", err), log)
			return
		}
		c.Request = c.Request.WithContext(contextWithIdentity(c.Request.Context(), identity))
		c.Next()
	}
}

// GinAuthorize checks that the authenticated identity may perform action.
// Must be used after GinRequireAuth. Aborts with 403/404 on denial.
func GinAuthorize(authorizer auth.Authorizer, action auth.Action) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := IdentityFromContext(c.Request.Context())
		if !ok {
			writeInternalError(c, errors.New("GinAuthorize called without identity in context"), slog.Default())
			return
		}
		if err := authorizer.Authorize(c.Request.Context(), identity, action); err != nil {
			writeServiceError(c, err, slog.Default())
			return
		}
		c.Next()
	}
}
