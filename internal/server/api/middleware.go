package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/auth"
)

// statusRecorder wraps ResponseWriter to capture the HTTP status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func loggingMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

func jsonContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// requireAuth validates the bearer token and injects *domain.Identity into context.
// Returns 401 if no/invalid token.
func requireAuth(authenticator auth.Authenticator, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, err := authenticator.Authenticate(r.Context(), r)
			if err != nil {
				if errors.Is(err, domain.ErrUnauthorized) {
					w.WriteHeader(http.StatusUnauthorized)
					json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
					return
				}
				log.Error("authentication error", "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
				return
			}
			ctx := contextWithIdentity(r.Context(), identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requireOrgAccess checks that the authenticated identity has permission for the action.
// The orgID is extracted from the path using the provided pathParam (e.g., "orgId").
// If pathParam is empty, the org check is skipped (for routes where org is implicit).
// Returns 403 if forbidden.
func requireOrgAccess(authorizer auth.Authorizer, action auth.Action, pathParam string, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := IdentityFromContext(r.Context())
			if !ok {
				// Programming error: requireAuth should have run first
				log.Error("requireOrgAccess called without identity in context")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
				return
			}

			var orgID uuid.UUID
			if pathParam != "" {
				orgIDStr := r.PathValue(pathParam)
				var err error
				orgID, err = uuid.Parse(orgIDStr)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]string{"error": "invalid organization ID"})
					return
				}
			} else {
				// No path param - use identity's org (for routes where org is implicit)
				orgID = identity.OrganizationID
			}

			if err := authorizer.Authorize(identity, action, orgID); err != nil {
				if errors.Is(err, domain.ErrForbidden) {
					w.WriteHeader(http.StatusForbidden)
					json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
					return
				}
				log.Error("authorization error", "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// chain composes multiple middleware functions.
func chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}
