package api

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/shared/server/api"
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

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
	}
	return h.Hijack()
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
				if errors.Is(err, api.ErrUnauthorized) {
					writeError(w, http.StatusUnauthorized, "unauthorized")
					return
				}
				log.Error("authentication error", "err", err)
				writeError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			ctx := contextWithIdentity(r.Context(), identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requireResourceAccess checks that the authenticated identity may perform action.
//
//   - orgParam:      path value key for the org ID (e.g. "orgId"). Empty means
//     the authorizer uses identity.OrganizationID.
//   - resourceParam: path value key for the specific resource being accessed
//     (e.g. "projectId", "deploymentId"). Empty means no resource-level check.
//
// The middleware populates action.OrgID and action.ResourceID from the path,
// then delegates all authorization logic (including resource ownership) to the
// authorizer. Returns 403 if forbidden, 404 if the resource does not exist.
func requireResourceAccess(
	authorizer auth.Authorizer,
	action auth.Action,
	orgParam string,
	resourceParam string,
	log *slog.Logger,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := IdentityFromContext(r.Context())
			if !ok {
				log.Error("requireResourceAccess called without identity in context")
				writeError(w, http.StatusInternalServerError, "internal server error")
				return
			}

			if orgParam != "" {
				orgID, err := uuid.Parse(r.PathValue(orgParam))
				if err != nil {
					writeError(w, http.StatusBadRequest, "invalid organization ID")
					return
				}
				action.OrgID = orgID
			}

			if resourceParam != "" {
				resourceID, err := uuid.Parse(r.PathValue(resourceParam))
				if err != nil {
					writeError(w, http.StatusBadRequest, "invalid resource ID")
					return
				}
				action.ResourceID = resourceID
			}

			if err := authorizer.Authorize(r.Context(), identity, action); err != nil {
				if errors.Is(err, api.ErrForbidden) {
					writeError(w, http.StatusForbidden, "forbidden")
					return
				}
				if errors.Is(err, api.ErrNotFound) {
					writeError(w, http.StatusNotFound, "not found")
					return
				}
				log.Error("authorization error", "err", err)
				writeError(w, http.StatusInternalServerError, "internal server error")
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
