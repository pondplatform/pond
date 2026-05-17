package db

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

type DeploymentStatus = api.DeploymentStatus

type Deployment struct {
	ID                    uuid.UUID                       `json:"id"`
	ServiceID             uuid.UUID                       `json:"serviceId"`
	EnvironmentID         uuid.UUID                       `json:"environmentId"`
	ImageTag              string                          `json:"imageTag"`
	ServiceConfigSnapshot serviceconfig.ServiceConfig     `json:"serviceConfigSnapshot"`
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

func (d Deployment) Validate() error {
	var errs api.ValidationErrors
	if d.ImageTag == "" {
		errs.Add("Deployment", "imageTag", "must not be empty")
	}
	if d.TriggeredBy == "" {
		errs.Add("Deployment", "triggeredBy", "must not be empty")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}

// DependencyDeploymentStatus represents the lifecycle of a tofu.apply command.
type DependencyDeploymentStatus string

const (
	DependencyDeploymentStatusAwaitingInput DependencyDeploymentStatus = "awaiting_input"
	DependencyDeploymentStatusPending       DependencyDeploymentStatus = "pending"
	DependencyDeploymentStatusSucceeded     DependencyDeploymentStatus = "succeeded"
	DependencyDeploymentStatusFailed        DependencyDeploymentStatus = "failed"
)
