package wire

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Envelope is the wire format for all WebSocket messages.
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// CommandType identifies the kind of work the agent should perform.
type CommandType = string

const (
	CommandHelmUpgrade CommandType = "helm.upgrade"
	CommandTofuApply   CommandType = "tofu.apply"
	CommandTofuOutput  CommandType = "tofu.output"
	CommandStatusQuery CommandType = "status.query"
)

// CommandPayload is the "command" message sent from server to agent.
type CommandPayload struct {
	ID           uuid.UUID       `json:"id"`
	DeploymentID uuid.UUID       `json:"deployment_id"`
	Type         CommandType     `json:"type"`
	Payload      json.RawMessage `json:"payload"`
	CreatedAt    time.Time       `json:"created_at"`
}

// AckPayload is the "ack" message sent from agent to server when it starts
// executing a command.
type AckPayload struct {
	CommandID    uuid.UUID `json:"command_id"`
	DeploymentID uuid.UUID `json:"deployment_id"`
}

// ResultPayload is the "result" message sent from agent to server after a
// command finishes.
type ResultPayload struct {
	CommandID    uuid.UUID       `json:"commandId"`
	DeploymentID uuid.UUID       `json:"deploymentId"`
	Success      bool            `json:"success"`
	Output       json.RawMessage `json:"output,omitempty"`
	Error        string          `json:"error,omitempty"`
}

// LogPayload is the "log" message sent from agent to server while a command
// is executing.
type LogPayload struct {
	CommandID uuid.UUID `json:"command_id"`
	Line      string    `json:"line"`
	Timestamp time.Time `json:"timestamp"`
	Stream    string    `json:"stream"` // "stdout" | "stderr"
}

// HelmUpgradePayload is the typed payload for CommandHelmUpgrade.
type HelmUpgradePayload struct {
	ReleaseName string `json:"releaseName"`
	Namespace   string `json:"namespace"`
	ChartPath   string `json:"chartPath"`
	Values      []byte `json:"values"` // YAML-marshalled helm values
}

// TofuApplyPayload is the typed payload for CommandTofuApply.
type TofuApplyPayload struct {
	WorkDir   string         `json:"workDir"`
	StatePath string         `json:"statePath"`
	Vars      map[string]any `json:"vars"`
}

// TofuOutputPayload is the typed payload for CommandTofuOutput.
type TofuOutputPayload struct {
	WorkDir   string `json:"workDir"`
	StatePath string `json:"statePath"`
}

// MarshalPayload JSON-encodes v and returns it as json.RawMessage.
func MarshalPayload(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return b, nil
}
