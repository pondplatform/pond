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
)

type Deployment struct {
	ID                    uuid.UUID
	ServiceID             uuid.UUID
	EnvironmentID         uuid.UUID
	ImageTag              string
	ServiceConfigSnapshot ServiceConfig
	DependencyConfigs     map[string]DeploymentDependencyConfig
	Status                DeploymentStatus
	TriggeredBy           string
	HelmCommandID         *uuid.UUID
	CreatedAt             time.Time
	CompletedAt           *time.Time
}

type DeploymentDependencyConfig struct {
	ID             uuid.UUID
	DependencyName string
	DependencyType string
	Managed        *bool
	ProviderInputs map[string]any
	UserConfig     map[string]any
	Outputs        map[string]any

	// Execution state
	Status      DependencyRequestStatus
	CommandID   *uuid.UUID
	Output      json.RawMessage
	CompletedAt *time.Time
}

// DependencyRequestStatus represents the lifecycle of a tofu.apply command.
type DependencyRequestStatus string

const (
	DependencyRequestAwaitingInput DependencyRequestStatus = "awaiting_input"
	DependencyRequestStatusPending   DependencyRequestStatus = "pending"
	DependencyRequestStatusSucceeded DependencyRequestStatus = "succeeded"
	DependencyRequestStatusFailed    DependencyRequestStatus = "failed"
)


