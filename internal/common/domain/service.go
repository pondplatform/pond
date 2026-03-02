package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Name      string
	CreatedAt time.Time
}

type ServiceRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*Service, error)
	GetByName(ctx context.Context, projectID uuid.UUID, name string) (*Service, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]Service, error)
	Create(ctx context.Context, svc *Service) error
}
