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
	ID                    uuid.UUID                       `json:"id"`
	ServiceID             uuid.UUID                       `json:"serviceId"`
	EnvironmentID         uuid.UUID                       `json:"environmentId"`
	ImageTag              string                          `json:"imageTag"`
	ServiceConfigSnapshot ServiceConfig                   `json:"serviceConfigSnapshot"`
	DependencyConfigs     map[string]DependencyDeployment `json:"dependencyConfigs"`
	Status                DeploymentStatus                `json:"status"`
	TriggeredBy           string                          `json:"triggeredBy"`
	HelmCommandID         *uuid.UUID                      `json:"helmCommandId"`
	CreatedAt             time.Time                       `json:"createdAt"`
	CompletedAt           *time.Time                      `json:"completedAt"`
}

type DependencyDeployment struct {
	ID             uuid.UUID                  `json:"id"`
	DeploymentId   uuid.UUID                  `json:"deploymentId"`
	DependencyName string                     `json:"dependencyName"`
	DependencyType string                     `json:"dependencyType"`
	Managed        *bool                      `json:"managed"`
	ProviderInputs map[string]any             `json:"providerInputs"`
	UserConfig     map[string]any             `json:"userConfig"`
	Status         DependencyDeploymentStatus `json:"status"`
	CommandID      *uuid.UUID                 `json:"commandId"`
	Output         json.RawMessage            `json:"output"`
	CompletedAt    *time.Time                 `json:"completedAt"`
}

// DependencyDeploymentStatus represents the lifecycle of a tofu.apply command.
type DependencyDeploymentStatus string

const (
	DependencyDeploymentStatusAwaitingInput DependencyDeploymentStatus = "awaiting_input"
	DependencyDeploymentStatusPending       DependencyDeploymentStatus = "pending"
	DependencyDeploymentStatusSucceeded     DependencyDeploymentStatus = "succeeded"
	DependencyDeploymentStatusFailed        DependencyDeploymentStatus = "failed"
)
