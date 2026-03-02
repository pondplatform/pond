package agent

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type CommandType string

const (
	CommandHelmUpgrade CommandType = "helm.upgrade"
	CommandTofuApply   CommandType = "tofu.apply"
	CommandTofuOutput  CommandType = "tofu.output"
	CommandStatusQuery CommandType = "status.query"
)

type Command struct {
	ID           uuid.UUID
	DeploymentID uuid.UUID
	Type         CommandType
	Payload      json.RawMessage
	CreatedAt    time.Time
}

type CommandResult struct {
	CommandID uuid.UUID
	Success   bool
	Output    json.RawMessage
	Error     string
}

type LogEntry struct {
	CommandID uuid.UUID
	Line      string
	Timestamp time.Time
	Stream    string // "stdout" | "stderr"
}
