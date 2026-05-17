package auth

import (
	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

// RoleAuthorizer implements role-based authorization.
// admin  → everything
// member → Read/Write on all resources except ResourceToken and VerbManage
// viewer → VerbRead on all resources except ResourceToken
type RoleAuthorizer struct{}

// NewRoleAuthorizer creates a new RoleAuthorizer.
func NewRoleAuthorizer() *RoleAuthorizer {
	return &RoleAuthorizer{}
}

// Authorize checks if the identity has permission to perform the action on the org.
func (a *RoleAuthorizer) Authorize(identity *domain.Identity, action Action, orgID uuid.UUID) error {
	// Admin key bypasses all org membership and permission checks.
	if identity.IsAdminKey {
		return nil
	}

	// Check org membership - identity must belong to the target org
	if identity.OrganizationID != orgID {
		return api.ErrForbidden
	}

	// Admin can do everything
	if identity.Role == domain.RoleAdmin {
		return nil
	}

	// Token management is admin-only
	if action.Resource == ResourceToken {
		return api.ErrForbidden
	}

	// VerbManage is admin-only (cluster creation, org-level ops)
	if action.Verb == VerbManage {
		return api.ErrForbidden
	}

	// Member can read and write
	if identity.Role == domain.RoleMember {
		return nil
	}

	// Viewer can only read
	if identity.Role == domain.RoleViewer {
		if action.Verb == VerbRead {
			return nil
		}
		return api.ErrForbidden
	}

	// Unknown role - deny by default
	return api.ErrForbidden
}
