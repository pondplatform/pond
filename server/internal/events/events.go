package events

import (
	"encoding/json"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
)

// CommandQueued is published to the per-cluster command.queued topic after a
// command is inserted into the commands table with status=queued. The agent
// handler subscribes to this topic to wake an idle connection.
type CommandQueued struct {
	ClusterID    uuid.UUID
	CommandID    uuid.UUID
	DeploymentID uuid.UUID
}

// CommandResult is published to TopicCommandResults by the agent handler
// after it updates the command row to status=succeeded or status=failed.
// The deployment service subscribes to advance the state machine.
type CommandResult struct {
	CommandID uuid.UUID
	Success   bool
	Output    json.RawMessage
	Error     string
}

// CommandLog is published to TopicCommandLogs when the agent streams a log
// line. Consumers may use it for real-time log tailing.
type CommandLog struct {
	CommandID uuid.UUID
	Line      string
}

// CommandDispatch is published to ClusterCommandDispatchTopic(clusterID) by
// the deployment service when a specific command should be forwarded to a
// connected agent. The agent handler subscribes and writes it to the WebSocket.
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
	CommandID uuid.UUID
}

// AgentDisconnected is published to TopicAgentDisconnected when an agent
// WebSocket closes. The deployment service requeues any dispatched commands
// for the cluster.
type AgentDisconnected struct {
	ClusterID uuid.UUID
}

// UserInputRequired is published to ProjectUserInputRequiredTopic(projectID)
// when a dependency is blocked waiting for user-provided input.
type UserInputRequired struct {
	DeploymentId   uuid.UUID
	DependencyName string
}

// UserInputProvided is published when a user provides input for all awaiting
// dependencies on a deployment.
type UserInputProvided struct {
	DeploymentID uuid.UUID
}

const (
	TopicCommandResults    = "command.results"
	TopicCommandLogs       = "command.logs"
	TopicAgentReady        = "agent.ready"
	TopicCommandStarted    = "command.started"
	TopicAgentDisconnected = "agent.disconnected"
	TopicUserInputProvided = "user_input.provided"
)

// ClusterCommandQueuedTopic returns the per-cluster topic for CommandQueued events.
func ClusterCommandQueuedTopic(clusterID uuid.UUID) string {
	return "cluster." + clusterID.String() + ".command.queued"
}

// ClusterCommandDispatchTopic returns the per-cluster topic for CommandDispatch events.
func ClusterCommandDispatchTopic(clusterID uuid.UUID) string {
	return "cluster." + clusterID.String() + ".command.dispatch"
}

// ProjectUserInputRequiredTopic returns the per-project topic for UserInputRequired events.
func ProjectUserInputRequiredTopic(projectID uuid.UUID) string {
	return "project." + projectID.String() + ".user_input.required"
}
