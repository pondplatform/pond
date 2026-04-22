package domain

import (
	"time"

	"github.com/google/uuid"
)

// OrgRole is the role of an API token within an organization.
type OrgRole string

const (
	RoleAdmin  OrgRole = "admin"
	RoleMember OrgRole = "member"
	RoleViewer OrgRole = "viewer"
)

// APIToken is a bearer credential scoped to an organization.
// The plaintext token is never persisted; TokenHash is SHA-256(plaintext).
type APIToken struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	TokenHash      string
	Role           OrgRole
	Description    string
	CreatedAt      time.Time
	LastUsedAt     *time.Time
	RevokedAt      *time.Time
}

// IsActive returns true if the token has not been revoked.
func (t *APIToken) IsActive() bool {
	return t.RevokedAt == nil
}

// Identity is the authenticated principal injected into request context.
// It is resolved from an APIToken after authentication.
type Identity struct {
	TokenID        uuid.UUID
	OrganizationID uuid.UUID
	Role           OrgRole
	Description    string
}
