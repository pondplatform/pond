package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Project struct {
	ID                uuid.UUID
	OrganizationID    uuid.UUID
	Name              string
	RootEnvironmentID *uuid.UUID
	CreatedAt         time.Time
}

type ProjectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*Project, error)
	GetByName(ctx context.Context, orgID uuid.UUID, name string) (*Project, error)
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]Project, error)
	Create(ctx context.Context, project *Project) error
	SetRootEnvironment(ctx context.Context, projectID, envID uuid.UUID) error
}
