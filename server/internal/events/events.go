package events

import (
	"encoding/json"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
)

// CommandQueued is published to the per-cluster CommandQueued topic after a
// command is inserted into the commands table with status=queued. The agent
// handler subscribes to this topic to wake an idle connection.
type CommandQueued struct {
	ClusterID    uuid.UUID
	CommandID    uuid.UUID
	DeploymentID uuid.UUID
}

// CommandResult is published to the CommandResults topic by the agent handler
// after it updates the command row to status=succeeded or status=failed.
// The deployment service subscribes to advance the state machine.
type CommandResult struct {
	CommandID uuid.UUID
	Success   bool
	Output    json.RawMessage
	Error     string
}

// CommandLog is published to the CommandLogs topic when the agent streams a log
// line. Consumers may use it for real-time log tailing.
type CommandLog struct {
	CommandID uuid.UUID
	Line      string
}

// CommandDispatch is published to the per-cluster CommandDispatch topic by
// the deployment service when a specific command should be forwarded to a
// connected agent. The agent handler subscribes and writes it to the WebSocket.
type CommandDispatch struct {
	Cmd *domain.Command
}

// AgentReady is published to the AgentReady topic by the agent handler when a
// connected agent is ready to receive its next command (on connect or after
// finishing one). The deployment service subscribes, dequeues the next
// command for that cluster, and publishes CommandDispatch in response.
type AgentReady struct {
	ClusterID uuid.UUID
}

// CommandStarted is published to the CommandStarted topic when the agent ACKs
// a command.
type CommandStarted struct {
	CommandID uuid.UUID
}

// AgentDisconnected is published to the AgentDisconnected topic when an agent
// WebSocket closes. The deployment service requeues any dispatched commands
// for the cluster.
type AgentDisconnected struct {
	ClusterID uuid.UUID
}

// UserInputProvided is published when a user provides input for all awaiting
// dependencies on a deployment.
type UserInputProvided struct {
	DeploymentID uuid.UUID
}

// topics is the single place where every domain topic's name, payload type
// and delivery mode is defined.
type topics struct {
	broker broker

	commandResults    *topic[CommandResult]
	commandLogs       *topic[CommandLog]
	agentReady        *topic[AgentReady]
	commandStarted    *topic[CommandStarted]
	agentDisconnected *topic[AgentDisconnected]
	userInputProvided *topic[UserInputProvided]
}

func newTopics(b broker) *topics {
	return &topics{
		broker:            b,
		commandResults:    newTopic[CommandResult](b, "command.results", work),
		commandLogs:       newTopic[CommandLog](b, "command.logs", fanout),
		agentReady:        newTopic[AgentReady](b, "agent.ready", work),
		commandStarted:    newTopic[CommandStarted](b, "command.started", work),
		agentDisconnected: newTopic[AgentDisconnected](b, "agent.disconnected", work),
		userInputProvided: newTopic[UserInputProvided](b, "user_input.provided", work),
	}
}

func (t *topics) commandQueued(clusterID uuid.UUID) *topic[CommandQueued] {
	return newTopic[CommandQueued](t.broker, "cluster."+clusterID.String()+".command.queued", work)
}

func (t *topics) commandDispatch(clusterID uuid.UUID) *topic[CommandDispatch] {
	return newTopic[CommandDispatch](t.broker, "cluster."+clusterID.String()+".command.dispatch", work)
}

func (t *topics) CommandResultsPublisher() Publisher[CommandResult]   { return t.commandResults }
func (t *topics) CommandResultsSubscriber() Subscriber[CommandResult] { return t.commandResults }

func (t *topics) CommandLogsPublisher() Publisher[CommandLog]   { return t.commandLogs }
func (t *topics) CommandLogsSubscriber() Subscriber[CommandLog] { return t.commandLogs }

func (t *topics) AgentReadyPublisher() Publisher[AgentReady]   { return t.agentReady }
func (t *topics) AgentReadySubscriber() Subscriber[AgentReady] { return t.agentReady }

func (t *topics) CommandStartedPublisher() Publisher[CommandStarted]   { return t.commandStarted }
func (t *topics) CommandStartedSubscriber() Subscriber[CommandStarted] { return t.commandStarted }

func (t *topics) AgentDisconnectedPublisher() Publisher[AgentDisconnected] {
	return t.agentDisconnected
}
func (t *topics) AgentDisconnectedSubscriber() Subscriber[AgentDisconnected] {
	return t.agentDisconnected
}

func (t *topics) UserInputProvidedPublisher() Publisher[UserInputProvided] {
	return t.userInputProvided
}
func (t *topics) UserInputProvidedSubscriber() Subscriber[UserInputProvided] {
	return t.userInputProvided
}

func (t *topics) CommandQueuedPublisher(clusterID uuid.UUID) Publisher[CommandQueued] {
	return t.commandQueued(clusterID)
}
func (t *topics) CommandQueuedSubscriber(clusterID uuid.UUID) Subscriber[CommandQueued] {
	return t.commandQueued(clusterID)
}

func (t *topics) CommandDispatchPublisher(clusterID uuid.UUID) Publisher[CommandDispatch] {
	return t.commandDispatch(clusterID)
}
func (t *topics) CommandDispatchSubscriber(clusterID uuid.UUID) Subscriber[CommandDispatch] {
	return t.commandDispatch(clusterID)
}
