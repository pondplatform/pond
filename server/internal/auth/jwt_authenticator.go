package auth

import (
	"context"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
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
// Returns api.ErrUnauthorized for any invalid, tampered, or malformed token.
func (a *JWTAuthenticator) Authenticate(_ context.Context, r *http.Request) (*domain.Identity, error) {
	raw := BearerToken(r)
	if raw == "" {
		return nil, api.ErrUnauthorized
	}

	token, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, api.ErrUnauthorized
		}
		return a.secret, nil
	}, jwt.WithoutClaimsValidation())

	if err != nil || !token.Valid {
		return nil, api.ErrUnauthorized
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, api.ErrUnauthorized
	}

	description, _ := claims["description"].(string)

	// Admin token: cross-org, full access.
	if isAdmin, _ := claims["is_admin"].(bool); isAdmin {
		return &domain.Identity{
			OrganizationID: uuid.Nil,
			Role:           domain.RoleAdmin,
			Description:    description,
			IsAdminKey:     true,
		}, nil
	}

	// Org-scoped token: must have org_id and role.
	orgIDStr, ok := claims["org_id"].(string)
	if !ok || orgIDStr == "" {
		return nil, api.ErrUnauthorized
	}
	orgID, err := uuid.Parse(orgIDStr)
	if err != nil {
		return nil, api.ErrUnauthorized
	}

	roleStr, ok := claims["role"].(string)
	if !ok || roleStr == "" {
		return nil, api.ErrUnauthorized
	}
	role := domain.OrgRole(roleStr)
	if role != domain.RoleAdmin && role != domain.RoleMember && role != domain.RoleViewer {
		return nil, api.ErrUnauthorized
	}

	return &domain.Identity{
		OrganizationID: orgID,
		Role:           role,
		Description:    description,
	}, nil
}
