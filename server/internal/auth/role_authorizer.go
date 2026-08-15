package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

// RoleAuthorizer implements role-based authorization.
// admin  → everything
// member → Read/Write on all resources except ResourceToken and VerbManage
// viewer → VerbRead on all resources except ResourceToken
type RoleAuthorizer struct {
	authzRepo AuthorizationRepository
}

// NewRoleAuthorizer creates a new RoleAuthorizer.
func NewRoleAuthorizer(authzRepo AuthorizationRepository) *RoleAuthorizer {
	return &RoleAuthorizer{authzRepo: authzRepo}
}

// Authorize checks whether the identity may perform action.
// If action.OrgID is set it is used as the target org; otherwise the identity's
// own org is used. If action.ResourceID is non-nil the authorizer resolves the
// resource's owning org and verifies it matches before checking role permissions.
func (a *RoleAuthorizer) Authorize(ctx context.Context, identity *domain.Identity, action Action) error {
	if identity.IsAdminKey {
		return nil
	}

	orgID := action.OrgID
	if orgID == uuid.Nil {
		orgID = identity.OrganizationID
	}

	if action.ResourceID != uuid.Nil {
		resourceOrgID, err := a.resolveResourceOrgID(ctx, action)
		if err != nil {
			return err
		}
		if resourceOrgID != orgID {
			return api.ErrForbidden
		}
	}

	if identity.OrganizationID != orgID {
		return api.ErrForbidden
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

func (a *RoleAuthorizer) resolveResourceOrgID(ctx context.Context, action Action) (uuid.UUID, error) {
	id := action.ResourceID
	switch action.Resource {
	case ResourceOrganization:
		return a.authzRepo.OrgIDForOrganization(ctx, id)
	case ResourceCluster:
		return a.authzRepo.OrgIDForCluster(ctx, id)
	case ResourceProject:
		return a.authzRepo.OrgIDForProject(ctx, id)
	case ResourceEnvironment:
		return a.authzRepo.OrgIDForEnvironment(ctx, id)
	case ResourceService:
		return a.authzRepo.OrgIDForService(ctx, id)
	case ResourceDeployment:
		return a.authzRepo.OrgIDForDeployment(ctx, id)
	case ResourceCommand:
		return a.authzRepo.OrgIDForCommand(ctx, id)
	default:
		return uuid.Nil, fmt.Errorf("resolveResourceOrgID: unsupported resource type %q", action.Resource)
	}
}
