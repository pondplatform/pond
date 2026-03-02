package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Environment struct {
	ID                     uuid.UUID
	ProjectID              uuid.UUID
	ParentEnvironmentID    *uuid.UUID
	Name                   string
	Namespace              string
	DefaultIngressBaseHost string
	ClusterID              uuid.UUID
	CreatedAt              time.Time
}

type EnvironmentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*Environment, error)
	GetByName(ctx context.Context, projectID uuid.UUID, name string) (*Environment, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]Environment, error)
	Create(ctx context.Context, env *Environment) error
}
