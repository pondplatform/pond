package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type DeploymentStatus string

const (
	DeploymentStatusPending   DeploymentStatus = "pending"
	DeploymentStatusRunning   DeploymentStatus = "running"
	DeploymentStatusSucceeded DeploymentStatus = "succeeded"
	DeploymentStatusFailed    DeploymentStatus = "failed"
)

type Deployment struct {
	ID                    uuid.UUID
	ServiceID             uuid.UUID
	EnvironmentID         uuid.UUID
	ImageTag              string
	ServiceConfigSnapshot ServiceConfig
	DependencyConfigs     map[string]DependencyDeployment
	Status                DeploymentStatus
	TriggeredBy           string
	HelmCommandID         *uuid.UUID
	CreatedAt             time.Time
	CompletedAt           *time.Time
}

type DependencyDeployment struct {
	ID             uuid.UUID
	DeploymentId   uuid.UUID
	DependencyName string
	DependencyType string
	Managed        *bool
	ProviderInputs map[string]any
	UserConfig     map[string]any
	Outputs        map[string]any

	// Execution state
	Status      DependencyDeploymentStatus
	CommandID   *uuid.UUID
	Output      json.RawMessage
	CompletedAt *time.Time
}

// DependencyDeploymentStatus represents the lifecycle of a tofu.apply command.
type DependencyDeploymentStatus string

const (
	DependencyDeploymentStatusAwaitingInput DependencyDeploymentStatus = "awaiting_input"
	DependencyDeploymentStatusPending       DependencyDeploymentStatus = "pending"
	DependencyDeploymentStatusSucceeded     DependencyDeploymentStatus = "succeeded"
	DependencyDeploymentStatusFailed        DependencyDeploymentStatus = "failed"
)
