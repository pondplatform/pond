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

// Message types. The agent opens every connection with hello. It asks for
// work with ready, and the server answers with command or idle. The agent
// reports with result and log. The server pushes cancel when the deployment
// of the command an agent runs has ended.
//
// The server handles an agent's messages in order, so its answer to a ready
// means it has stored everything the agent sent before that ready.
const (
	MessageHello   = "hello"
	MessageReady   = "ready"
	MessageCommand = "command"
	MessageIdle    = "idle"
	MessageResult  = "result"
	MessageLog     = "log"
	MessageCancel  = "cancel"
)

// HelloPayload is the first message on every connection: what the agent
// holds, so the server can settle the commands it has as dispatched.
type HelloPayload struct {
	// Restarted is true until the server has answered the agent once since it
	// started: it holds nothing from before, so whatever it ran then is lost.
	Restarted bool `json:"restarted"`
	// Running is the command the agent runs, if any.
	Running *uuid.UUID `json:"running,omitempty"`
	// Finished are the commands whose results the agent sends next.
	Finished []uuid.UUID `json:"finished,omitempty"`
}

// CancelPayload is the "cancel" message: the agent should stop the command.
type CancelPayload struct {
	CommandID uuid.UUID `json:"commandId"`
}

// CommandType identifies the kind of work the agent should perform.
type CommandType = string

const (
	CommandHelmUpgrade CommandType = "helm.upgrade"
	CommandTofuApply   CommandType = "tofu.apply"
	CommandTofuOutput  CommandType = "tofu.output"
	// CommandServiceDelete tears a service down in one environment: its helm
	// release first, then each managed dependency, in that order.
	CommandServiceDelete CommandType = "service.delete"
)

// CommandPayload is the "command" message sent from server to agent.
type CommandPayload struct {
	ID           uuid.UUID       `json:"id"`
	DeploymentID uuid.UUID       `json:"deployment_id"`
	Type         CommandType     `json:"type"`
	Payload      json.RawMessage `json:"payload"`
	CreatedAt    time.Time       `json:"created_at"`
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

// ServiceDeletePayload is the typed payload for CommandServiceDelete. Release
// is nil when the service was never rolled out in the environment.
type ServiceDeletePayload struct {
	Release      *HelmRelease         `json:"release,omitempty"`
	Dependencies []TofuDestroyPayload `json:"dependencies,omitempty"`
}

// HelmRelease names an installed helm release.
type HelmRelease struct {
	ReleaseName string `json:"releaseName"`
	Namespace   string `json:"namespace"`
}

// TofuDestroyPayload destroys what a tofu.apply created. WorkDir, StatePath
// and Vars are the apply's own: the module derives resource names from Vars.
type TofuDestroyPayload struct {
	Name      string         `json:"name"` // the dependency's name, for messages
	WorkDir   string         `json:"workDir"`
	StatePath string         `json:"statePath"`
	Vars      map[string]any `json:"vars"`
}

// MarshalPayload JSON-encodes v and returns it as json.RawMessage.
func MarshalPayload(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return b, nil
}
