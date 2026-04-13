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

type Command struct {
	ID           uuid.UUID
	ClusterID    uuid.UUID
	DeploymentID uuid.UUID
	Type         string
	Payload      json.RawMessage
	Status       CommandStatus
	Output       json.RawMessage
	Error        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
