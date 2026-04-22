package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/store"
)

// TokenAuthenticator resolves Bearer tokens via the APITokenRepository.
type TokenAuthenticator struct {
	tokens store.APITokenRepository
}

// NewTokenAuthenticator creates a new TokenAuthenticator.
func NewTokenAuthenticator(tokens store.APITokenRepository) *TokenAuthenticator {
	return &TokenAuthenticator{tokens: tokens}
}

// Authenticate validates the bearer token and returns the identity.
// Returns domain.ErrUnauthorized if the token is missing, invalid, or revoked.
func (a *TokenAuthenticator) Authenticate(ctx context.Context, r *http.Request) (*domain.Identity, error) {
	token := BearerToken(r)
	if token == "" {
		return nil, domain.ErrUnauthorized
	}

	hash := SHA256Hex(token)
	apiToken, err := a.tokens.GetByTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrUnauthorized
		}
		return nil, err
	}

	// Fire and forget: update last used timestamp
	go func() {
		_ = a.tokens.UpdateLastUsed(context.Background(), apiToken.ID, time.Now())
	}()

	return &domain.Identity{
		TokenID:        apiToken.ID,
		OrganizationID: apiToken.OrganizationID,
		Role:           apiToken.Role,
		Description:    apiToken.Description,
	}, nil
}
