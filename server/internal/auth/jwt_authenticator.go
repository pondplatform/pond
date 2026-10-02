package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

// JWTAuthenticator validates HS256-signed JWTs and resolves an Identity from claims.
// Fully stateless — no database lookup on the hot path.
type JWTAuthenticator struct {
	secret []byte
}

// NewJWTAuthenticator creates a JWTAuthenticator using the given HMAC secret.
func NewJWTAuthenticator(secret []byte) *JWTAuthenticator {
	return &JWTAuthenticator{secret: secret}
}

// Authenticate parses and verifies the JWT bearer token and returns the resolved Identity.
// Returns an error wrapping api.ErrUnauthorized for any invalid, tampered, or malformed token.
func (a *JWTAuthenticator) Authenticate(_ context.Context, r *http.Request) (*domain.Identity, error) {
	raw := BearerToken(r)
	if raw == "" {
		if r.Header.Get("Authorization") == "" {
			return nil, api.Unauthorized(`missing API token: send an "Authorization: Bearer <token>" header`)
		}
		return nil, api.Unauthorized(`malformed Authorization header: expected "Bearer <token>"`)
	}

	token, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return a.secret, nil
	}, jwt.WithoutClaimsValidation())

	if err != nil {
		return nil, api.Unauthorized("invalid API token: %v", err)
	}
	if !token.Valid {
		return nil, api.Unauthorized("invalid API token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, api.Unauthorized("invalid API token: unexpected claims")
	}

	description, _ := claims["description"].(string)

	// Admin token: full access.
	if isAdmin, _ := claims["is_admin"].(bool); isAdmin {
		return &domain.Identity{
			Role:        domain.RoleAdmin,
			Description: description,
			IsAdminKey:  true,
		}, nil
	}

	// Role-scoped token: must have role.
	roleStr, _ := claims["role"].(string)
	role := domain.OrgRole(roleStr)
	if role != domain.RoleAdmin && role != domain.RoleMember && role != domain.RoleViewer {
		return nil, api.Unauthorized("API token has no valid role (got %q); issue a new token with role admin, member or viewer", roleStr)
	}

	return &domain.Identity{
		Role:        role,
		Description: description,
	}, nil
}
