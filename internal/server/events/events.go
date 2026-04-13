package events

import (
	"encoding/json"

	"github.com/google/uuid"
)

// CommandQueued is published to the per-cluster topic after a command is
// inserted into the commands table with status=queued. The agent handler
// subscribes to this topic to dispatch commands to connected agents.
type CommandQueued struct {
	ClusterID    uuid.UUID
	CommandID    uuid.UUID
	DeploymentID uuid.UUID
}

// CommandResult is published to TopicCommandResults by the agent handler
// after it updates the command row to status=succeeded or status=failed.
// The deployment service subscribes to advance the state machine.
type CommandResult struct {
	CommandID    uuid.UUID
	DeploymentID uuid.UUID
	Success      bool
	Output       json.RawMessage
	Error        string
}

// CommandLog is published to TopicCommandLogs when the agent streams a log
// line. Consumers may use it for real-time log tailing.
type CommandLog struct {
	CommandID uuid.UUID
	Line      string
}

const (
	TopicCommandResults = "command_results"
	TopicCommandLogs    = "command_logs"
)

// ClusterTopic returns the per-cluster notification topic string.
func ClusterTopic(clusterID uuid.UUID) string {
	return "cluster/" + clusterID.String()
}
