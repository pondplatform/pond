package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type DeploymentStatus string

const (
	DeploymentStatusPending       DeploymentStatus = "pending"
	DeploymentStatusRunning       DeploymentStatus = "running"
	DeploymentStatusSucceeded     DeploymentStatus = "succeeded"
	DeploymentStatusFailed        DeploymentStatus = "failed"
	DeploymentStatusAwaitingInput DeploymentStatus = "awaiting_input"
)

type Deployment struct {
	ID                    uuid.UUID
	ServiceID             uuid.UUID
	EnvironmentID         uuid.UUID
	ImageTag              string
	ServiceConfigSnapshot ServiceConfig
	Status                DeploymentStatus
	TriggeredBy           string
	CreatedAt             time.Time
	CompletedAt           *time.Time
}

type DeploymentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*Deployment, error)
	ListByService(ctx context.Context, serviceID uuid.UUID) ([]Deployment, error)
	Create(ctx context.Context, d *Deployment) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status DeploymentStatus, completedAt *time.Time) error
}
