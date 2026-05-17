package api

import (
	"context"

	domain "github.com/pondplatform/pond/server/internal/model/db"
)

type contextKey int

const identityKey contextKey = 0

// IdentityFromContext retrieves the authenticated identity from the context.
// Returns nil and false if no identity is present.
func IdentityFromContext(ctx context.Context) (*domain.Identity, bool) {
	id, ok := ctx.Value(identityKey).(*domain.Identity)
	return id, ok
}

// contextWithIdentity returns a new context with the identity attached.
func contextWithIdentity(ctx context.Context, id *domain.Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}
