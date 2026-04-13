package domain

import (
	"encoding/json"
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
	HelmCommandID         *uuid.UUID
	CreatedAt             time.Time
	CompletedAt           *time.Time
}

// DependencyRequestStatus represents the lifecycle of a tofu.apply command.
type DependencyRequestStatus string

const (
	DependencyRequestStatusPending   DependencyRequestStatus = "pending"
	DependencyRequestStatusSucceeded DependencyRequestStatus = "succeeded"
	DependencyRequestStatusFailed    DependencyRequestStatus = "failed"
)

// DependencyDeploymentRequest tracks a tofu.apply command enqueued for a
// managed dependency as part of a deployment.
type DependencyDeploymentRequest struct {
	ID             uuid.UUID
	DeploymentID   uuid.UUID
	CommandID      uuid.UUID
	DependencyName string
	Status         DependencyRequestStatus
	Output         json.RawMessage // tofu outputs once succeeded
	CompletedAt    *time.Time
}


