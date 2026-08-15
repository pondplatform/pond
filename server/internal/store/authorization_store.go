package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/shared/server/api"
)

// AuthorizationStore implements auth.AuthorizationRepository. Each method runs
// a single optimized query to resolve the owning organization for a resource.
type AuthorizationStore struct {
	db DBTX
}

func NewAuthorizationStore(db DBTX) *AuthorizationStore {
	return &AuthorizationStore{db: db}
}

func (s *AuthorizationStore) OrgIDForOrganization(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.scanOrgID(ctx, id,
		"SELECT id FROM organizations WHERE id = $1",
		auth.ResourceOrganization,
	)
}

func (s *AuthorizationStore) OrgIDForCluster(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.scanOrgID(ctx, id,
		"SELECT organization_id FROM clusters WHERE id = $1",
		auth.ResourceCluster,
	)
}

func (s *AuthorizationStore) OrgIDForProject(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.scanOrgID(ctx, id,
		"SELECT organization_id FROM projects WHERE id = $1",
		auth.ResourceProject,
	)
}

func (s *AuthorizationStore) OrgIDForEnvironment(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.scanOrgID(ctx, id,
		`SELECT p.organization_id
		 FROM environments e
		 JOIN projects p ON p.id = e.project_id
		 WHERE e.id = $1`,
		auth.ResourceEnvironment,
	)
}

func (s *AuthorizationStore) OrgIDForService(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.scanOrgID(ctx, id,
		`SELECT p.organization_id
		 FROM services s
		 JOIN projects p ON p.id = s.project_id
		 WHERE s.id = $1`,
		auth.ResourceService,
	)
}

func (s *AuthorizationStore) OrgIDForDeployment(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.scanOrgID(ctx, id,
		`SELECT p.organization_id
		 FROM deployments d
		 JOIN services s ON s.id = d.service_id
		 JOIN projects p ON p.id = s.project_id
		 WHERE d.id = $1`,
		auth.ResourceDeployment,
	)
}

func (s *AuthorizationStore) OrgIDForCommand(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.scanOrgID(ctx, id,
		`SELECT p.organization_id
		 FROM commands c
		 JOIN deployments d ON d.id = c.deployment_id
		 JOIN services s ON s.id = d.service_id
		 JOIN projects p ON p.id = s.project_id
		 WHERE c.id = $1`,
		auth.ResourceCommand,
	)
}

// scanOrgID runs a single-row query that returns one UUID column and wraps
// sql.ErrNoRows as api.ErrNotFound.
func (s *AuthorizationStore) scanOrgID(ctx context.Context, id uuid.UUID, query string, resource auth.ResourceType) (uuid.UUID, error) {
	var orgID uuid.UUID
	err := s.db.QueryRowContext(ctx, query, id).Scan(&orgID)
	if err == sql.ErrNoRows {
		return uuid.Nil, fmt.Errorf("resource %s %s: %w", resource, id, api.ErrNotFound)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("orgIDFor%s %s: %w", resource, id, err)
	}
	return orgID, nil
}
