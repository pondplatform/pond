package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Organization struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

type OrganizationRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*Organization, error)
	GetByName(ctx context.Context, name string) (*Organization, error)
	Create(ctx context.Context, org *Organization) error
}
