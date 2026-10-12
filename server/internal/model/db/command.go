package db

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
)

// Command types
const (
	CommandTypeTofuApply   = "tofu.apply"
	CommandTypeHelmUpgrade = "helm.upgrade"
)

// Command is one unit of work for an agent. A deployment's commands run in
// phases: one becomes available only once every command of the same
// deployment in a lower phase has succeeded.
type Command struct {
	ID           uuid.UUID `json:"id"`
	ClusterID    uuid.UUID `json:"clusterId"`
	DeploymentID uuid.UUID `json:"deploymentId"`
	Type         string    `json:"type"`
	Phase        int       `json:"phase"`
	// Payload is nil until the command's phase starts, when it depends on the
	// outputs of an earlier phase.
	Payload   json.RawMessage `json:"payload"`
	Status    CommandStatus   `json:"status"`
	Output    json.RawMessage `json:"output"`
	Error     string          `json:"error"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type CommandLog struct {
	Line     string    `json:"line"`
	LoggedAt time.Time `json:"loggedAt"`
}
