package auth

import (
	"context"
	"net/http"

	domain "github.com/pondplatform/pond/server/internal/model/db"
)

// AdminKeyAuthenticator wraps another Authenticator and grants full admin
// access when the bearer token matches the configured admin key.
type AdminKeyAuthenticator struct {
	adminKey string
	inner    Authenticator
}

// NewAdminKeyAuthenticator returns an AdminKeyAuthenticator. If adminKey is
// empty the wrapper is a no-op passthrough.
func NewAdminKeyAuthenticator(adminKey string, inner Authenticator) *AdminKeyAuthenticator {
	return &AdminKeyAuthenticator{adminKey: adminKey, inner: inner}
}

// Authenticate checks for the admin key first, then falls through to the
// wrapped authenticator.
func (a *AdminKeyAuthenticator) Authenticate(ctx context.Context, r *http.Request) (*domain.Identity, error) {
	if a.adminKey != "" {
		token := BearerToken(r)
		if token == a.adminKey {
			return &domain.Identity{
				Role:        domain.RoleAdmin,
				Description: "admin key",
				IsAdminKey:  true,
			}, nil
		}
	}
	return a.inner.Authenticate(ctx, r)
}
