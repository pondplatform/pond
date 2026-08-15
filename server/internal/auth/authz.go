package auth

import (
	"context"

	"github.com/google/uuid"
)

// AuthorizationRepository provides per-resource-type org ownership lookups.
// The Authorizer is responsible for calling the correct method based on the
// action's resource type.
type AuthorizationRepository interface {
	OrgIDForOrganization(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
	OrgIDForCluster(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
	OrgIDForProject(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
	OrgIDForEnvironment(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
	OrgIDForService(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
	OrgIDForDeployment(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
	OrgIDForCommand(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
}
