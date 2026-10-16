package db

import (
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// DeploymentState is the stored lifecycle of a deployment. Anything finer,
// such as whether an agent has started on it, is computed from its commands.
type DeploymentState string

const (
	DeploymentInProgress DeploymentState = "in_progress"
	DeploymentSucceeded  DeploymentState = "succeeded"
	DeploymentFailed     DeploymentState = "failed"
	DeploymentCancelled  DeploymentState = "cancelled"
)

type Deployment struct {
	ID                    uuid.UUID                   `json:"id"`
	ServiceID             uuid.UUID                   `json:"serviceId"`
	EnvironmentID         uuid.UUID                   `json:"environmentId"`
	ImageTag              string                      `json:"imageTag"`
	ServiceConfigSnapshot serviceconfig.ServiceConfig `json:"serviceConfigSnapshot"`
	Status                DeploymentState             `json:"status"`
	TriggeredBy           string                      `json:"triggeredBy"`
	CreatedAt             time.Time                   `json:"createdAt"`
	CompletedAt           *time.Time                  `json:"completedAt"`
	Error                 *string                     `json:"error,omitempty"`
	// Variables include secret values, so they are never encoded.
	Variables map[string]string `json:"-"`
	// Exports are the values other services may import, set at rollout; nil
	// before then and for deployments that predate them.
	Exports map[string]string `json:"-"`
	// Started and Stopping are computed, not stored. Started: an agent has
	// picked up one of the deployment's commands. Stopping: an agent is still
	// running one, though the deployment may already have ended.
	Started  bool `json:"-"`
	Stopping bool `json:"-"`
}

// APIStatus is the status users see: pending until an agent starts on the
// deployment, then running, then its outcome. A cancelled deployment shows as
// cancelling until its agents have stopped what they were running.
func (d Deployment) APIStatus() api.DeploymentStatus {
	switch d.Status {
	case DeploymentSucceeded:
		return api.DeploymentStatusSucceeded
	case DeploymentFailed:
		return api.DeploymentStatusFailed
	case DeploymentCancelled:
		if d.Stopping {
			return api.DeploymentStatusCancelling
		}
		return api.DeploymentStatusCancelled
	}
	if d.Started {
		return api.DeploymentStatusRunning
	}
	return api.DeploymentStatusPending
}

// DependencyDeployment is a dependency as one deployment resolved it. It does
// not change after submit: a managed dependency's progress and outputs are
// those of its command, and an unmanaged one's outputs are its UserConfig.
type DependencyDeployment struct {
	ID             uuid.UUID      `json:"id"`
	DeploymentId   uuid.UUID      `json:"deploymentId"`
	DependencyName string         `json:"dependencyName"`
	DependencyType string         `json:"dependencyType"`
	Managed        bool           `json:"managed"`
	ProviderInputs map[string]any `json:"providerInputs"`
	UserConfig     map[string]any `json:"userConfig"`
	CommandID      *uuid.UUID     `json:"commandId"`
}

// APIStatus follows the dependency's command, given as cmd; an unmanaged
// dependency has none and is ready from the start.
func (d DependencyDeployment) APIStatus(cmd *Command) api.DependencyDeploymentStatus {
	if !d.Managed {
		return api.DependencyDeploymentStatusSucceeded
	}
	if cmd == nil {
		return api.DependencyDeploymentStatusPending
	}
	switch cmd.Status {
	case CommandStatusSucceeded:
		return api.DependencyDeploymentStatusSucceeded
	case CommandStatusFailed:
		return api.DependencyDeploymentStatusFailed
	case CommandStatusCancelled:
		return api.DependencyDeploymentStatusCancelled
	}
	return api.DependencyDeploymentStatusPending
}
