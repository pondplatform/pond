package auth

import (
	"context"

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

// Authorize checks whether the identity may perform action.
func (a *RoleAuthorizer) Authorize(_ context.Context, identity *domain.Identity, action Action) error {
	if identity.IsAdminKey {
		return nil
	}

	if identity.Role == domain.RoleAdmin {
		return nil
	}

	if action.Resource == ResourceToken {
		return api.ErrForbidden
	}

	if action.Verb == VerbManage {
		return api.ErrForbidden
	}

	if identity.Role == domain.RoleMember {
		return nil
	}

	if identity.Role == domain.RoleViewer {
		if action.Verb == VerbRead {
			return nil
		}
		return api.ErrForbidden
	}

	return api.ErrForbidden
}
