package domain

import (
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

