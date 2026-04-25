package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type CommandStatus string

const (
	CommandStatusQueued     CommandStatus = "queued"
	CommandStatusDispatched CommandStatus = "dispatched"
	CommandStatusSucceeded  CommandStatus = "succeeded"
	CommandStatusFailed     CommandStatus = "failed"
	CommandStatusCancelled  CommandStatus = "cancelled"
	CommandStatusTimedOut   CommandStatus = "timed_out"
)

// Command types
const (
	CommandTypeTofuApply   = "tofu.apply"
	CommandTypeHelmUpgrade = "helm.upgrade"
)

type Command struct {
	ID           uuid.UUID       `json:"id"`
	ClusterID    uuid.UUID       `json:"clusterId"`
	DeploymentID uuid.UUID       `json:"deploymentId"`
	Type         string          `json:"type"`
	Payload      json.RawMessage `json:"payload"`
	Status       CommandStatus   `json:"status"`
	Output       json.RawMessage `json:"output"`
	Error        string          `json:"error"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}
