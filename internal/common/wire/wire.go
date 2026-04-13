// Package wire defines the WebSocket message types shared between the server
// and agent binaries.
package wire

import (
	"encoding/json"
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
	CommandID    uuid.UUID       `json:"command_id"`
	DeploymentID uuid.UUID       `json:"deployment_id"`
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
