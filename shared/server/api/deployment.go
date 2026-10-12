package api

import (
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

type DeploymentStatus string

const (
	DeploymentStatusPending   DeploymentStatus = "pending"
	DeploymentStatusRunning   DeploymentStatus = "running"
	DeploymentStatusSucceeded DeploymentStatus = "succeeded"
	DeploymentStatusFailed    DeploymentStatus = "failed"
	// DeploymentStatusCancelling is a cancelled deployment whose agent is
	// still stopping the command it was running.
	DeploymentStatusCancelling DeploymentStatus = "cancelling"
	DeploymentStatusCancelled  DeploymentStatus = "cancelled"
)

type CommandStatus string

const (
	CommandStatusQueued     CommandStatus = "queued"
	CommandStatusDispatched CommandStatus = "dispatched"
	CommandStatusSucceeded  CommandStatus = "succeeded"
	CommandStatusFailed     CommandStatus = "failed"
	CommandStatusCancelled  CommandStatus = "cancelled"
)

type DependencyDeploymentStatus string

const (
	DependencyDeploymentStatusPending   DependencyDeploymentStatus = "pending"
	DependencyDeploymentStatusSucceeded DependencyDeploymentStatus = "succeeded"
	DependencyDeploymentStatusFailed    DependencyDeploymentStatus = "failed"
	DependencyDeploymentStatusCancelled DependencyDeploymentStatus = "cancelled"
)

type CommandSummary struct {
	ID        uuid.UUID     `json:"id"`
	Type      string        `json:"type"`
	Status    CommandStatus `json:"status"`
	Error     string        `json:"error,omitempty"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

type DependencyDeploymentSummary struct {
	Name        string                     `json:"name"`
	Type        string                     `json:"type"`
	Managed     *bool                      `json:"managed"`
	Status      DependencyDeploymentStatus `json:"status"`
	CommandID   *uuid.UUID                 `json:"commandId,omitempty"`
	CompletedAt *time.Time                 `json:"completedAt,omitempty"`
}

type Deployment struct {
	ID            uuid.UUID                     `json:"id"`
	ServiceID     uuid.UUID                     `json:"serviceId"`
	EnvironmentID uuid.UUID                     `json:"environmentId"`
	ImageTag      string                        `json:"imageTag"`
	TriggeredBy   string                        `json:"triggeredBy"`
	Status        DeploymentStatus              `json:"status"`
	CreatedAt     time.Time                     `json:"createdAt"`
	CompletedAt   *time.Time                    `json:"completedAt,omitempty"`
	Error         *string                       `json:"error,omitempty"`
	Dependencies  []DependencyDeploymentSummary `json:"dependencies"`
	Commands      []CommandSummary              `json:"commands"`
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
	Dependencies      map[string]DependencyInput      `json:"dependencies"`
}

type DependencyInput struct {
	Managed bool           `json:"managed"`
	Values  map[string]any `json:"values"`
}

// ConfigureDeploymentRequest is the format of a dependency input file, as
// passed to `pond deploy --input`.
type ConfigureDeploymentRequest struct {
	Dependencies map[string]DependencyInput `json:"dependencies"`
}
