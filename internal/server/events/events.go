package events

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
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

// CommandDispatch is published to ClusterTopic(clusterID) by the deployment
// service when a specific command should be forwarded to a connected agent.
// The agent handler subscribes and writes it to the WebSocket.
type CommandDispatch struct {
	Cmd *domain.Command
}

// AgentReady is published to TopicAgentReady by the agent handler when a
// connected agent is ready to receive its next command (on connect or after
// finishing one). The deployment service subscribes, dequeues the next
// command for that cluster, and publishes CommandDispatch in response.
type AgentReady struct {
	ClusterID uuid.UUID
}

// CommandStarted is published to TopicCommandStarted when the agent ACKs a
// command — the deployment service subscribes to transition the deployment
// from pending → running.
type CommandStarted struct {
	DeploymentID uuid.UUID
}

// AgentDisconnected is published to TopicAgentDisconnected when an agent
// WebSocket closes. If InFlightCommandID is non-nil, the deployment service
// requeues that command for redelivery.
type AgentDisconnected struct {
	ClusterID         uuid.UUID
	InFlightCommandID uuid.UUID
}


// CommandLog is published to TopicCommandLogs when the agent streams a log
// line. Consumers may use it for real-time log tailing.
type UserInputRequired struct {
	DeploymentId uuid.UUID
	DependencyName      string
}

const (
	TopicCommandResults    = "command_results"
	TopicCommandLogs       = "command_logs"
	TopicAgentReady        = "agent_ready"
	TopicCommandStarted    = "command_started"
	TopicAgentDisconnected = "agent_disconnected"
	TopicUserInputRequired = "user_input_required"
)

// ClusterTopic returns the per-cluster notification topic string.
func ClusterTopic(clusterID uuid.UUID) string {
	return "cluster/" + clusterID.String()
}
