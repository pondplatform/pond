package api

import (
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

type DeploymentStatus string

const (
	DeploymentStatusPending       DeploymentStatus = "pending"
	DeploymentStatusRunning       DeploymentStatus = "running"
	DeploymentStatusSucceeded     DeploymentStatus = "succeeded"
	DeploymentStatusAwaitingInput DeploymentStatus = "awaiting_input"
	DeploymentStatusFailed        DeploymentStatus = "failed"
)

type Deployment struct {
	ID            uuid.UUID        `json:"id"`
	ServiceID     uuid.UUID        `json:"serviceId"`
	EnvironmentID uuid.UUID        `json:"environmentId"`
	Status        DeploymentStatus `json:"status"`
	CreatedAt     time.Time        `json:"createdAt"`
}

type DeploymentListItem struct {
	ID            uuid.UUID        `json:"id"`
	ServiceID     uuid.UUID        `json:"serviceId"`
	EnvironmentID uuid.UUID        `json:"environmentId"`
	ImageTag      string           `json:"imageTag"`
	Status        DeploymentStatus `json:"status"`
	TriggeredBy   string           `json:"triggeredBy"`
	CreatedAt     time.Time        `json:"createdAt"`
	CompletedAt   *time.Time       `json:"completedAt"`
}

type SubmitRequest struct {
	ProjectID         uuid.UUID                       `json:"projectId"`
	EnvironmentName   string                          `json:"environmentName"`
	OverridableConfig serviceconfig.OverridableConfig `json:"overridableConfig"`
	ImageTag          string                          `json:"imageTag"`
	TriggeredBy       string                          `json:"triggeredBy"`
	CreateIfNotExists bool                            `json:"createIfNotExists"`
}

type DependencyInput struct {
	Managed bool           `json:"managed"`
	Values  map[string]any `json:"values"`
}

type ConfigureDeploymentRequest struct {
	Dependencies map[string]DependencyInput `json:"dependencies"`
}
