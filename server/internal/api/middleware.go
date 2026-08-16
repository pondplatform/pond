package api

import (
	"errors"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/shared/server/api"
)

// GinLogger is a gin middleware that logs each request using slog.
func GinLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
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
				c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
				return
			}
			log.Error("authentication error", "err", err)
			c.AbortWithStatusJSON(500, gin.H{"error": "internal server error"})
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
			log := slog.Default()
			log.Error("GinAuthorize called without identity in context")
			c.AbortWithStatusJSON(500, gin.H{"error": "internal server error"})
			return
		}
		if err := authorizer.Authorize(c.Request.Context(), identity, action); err != nil {
			if errors.Is(err, api.ErrForbidden) {
				c.AbortWithStatusJSON(403, gin.H{"error": "forbidden"})
				return
			}
			if errors.Is(err, api.ErrNotFound) {
				c.AbortWithStatusJSON(404, gin.H{"error": "not found"})
				return
			}
			slog.Default().Error("authorization error", "err", err)
			c.AbortWithStatusJSON(500, gin.H{"error": "internal server error"})
			return
		}
		c.Next()
	}
}
