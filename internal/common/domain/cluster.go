package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Cluster struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Name           string
	AgentTokenHash string
	LastSeenAt     *time.Time
	CreatedAt      time.Time
}

type ClusterRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*Cluster, error)
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]Cluster, error)
	Create(ctx context.Context, cluster *Cluster) error
	UpdateLastSeen(ctx context.Context, id uuid.UUID, t time.Time) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*Cluster, error)
}
