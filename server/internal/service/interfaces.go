package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// DeploymentDetail is the enriched view of a deployment returned by GetStatus.
// It includes the dependency states and their associated commands.
type DeploymentDetail struct {
	Deployment   *domain.Deployment
	Dependencies []domain.DependencyDeployment
	Commands     []*domain.Command
}

type DeploymentService interface {
	Submit(ctx context.Context, req api.SubmitRequest) (*domain.Deployment, error)
	GetStatus(ctx context.Context, deploymentID uuid.UUID) (*DeploymentDetail, error)
	Validate(ctx context.Context, req api.SubmitRequest) (*ValidationResult, error)
	// ListByService returns deployments for a service, optionally filtered by environment and status.
	ListByService(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *domain.DeploymentStatus, limit int, cursor string) ([]domain.Deployment, error)
	// Cancel cancels an in-progress deployment. Returns an error if the deployment is already in a terminal state.
	Cancel(ctx context.Context, deploymentID uuid.UUID) error
	// ConfigureDeployment provides user input for all awaiting dependencies in
	// one call and publishes a UserInputProvided event to trigger scheduling.
	ConfigureDeployment(ctx context.Context, deploymentID uuid.UUID, inputs map[string]api.DependencyInput) error
	// GetCommandLogs returns all log lines for a command in chronological order.
	// Returns api.ErrNotFound if the command does not exist.
	GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]domain.CommandLog, error)
	// Start registers subscriptions on the event bus (agent.ready,
	// command.started, command.results, command.logs, agent.disconnected,
	// user_input.provided) and drives the deployment state machine in response.
	// Returns an error if any subscription fails. Blocks until ctx is cancelled.
	Start(ctx context.Context) error
}


type SubmitRequest = api.SubmitRequest

type ValidationResult struct {
	Valid    bool
	Errors   []api.ValidationError
	Warnings []api.ValidationWarning
}

// TxRepos holds the writable repositories needed within a single transaction.
type TxRepos struct {
	DeploymentInfo store.DeploymentInfoStore
}

// Transactor executes fn inside a database transaction, rolling back on error.
type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx TxRepos) error) error
}

// PendingDep groups a queued tofu.apply command with its dependency config row.
type PendingDep struct {
	Cmd    *domain.Command
	DepCfg *domain.DependencyDeployment
}

// DependencyService handles dependency command scheduling and context resolution.
type DependencyService interface {
	// ScheduleCommands queues tofu.apply commands for all managed deps in the
	// deployment and creates DependencyDeploymentRequest rows inside tx.
	ScheduleCommands(ctx context.Context, tx TxRepos, service *domain.Service, environment *domain.Environment, dep *domain.Deployment) ([]domain.DependencyDeployment, error)

	// ScheduleAfterInput processes a dependency after user input is provided.
	// For managed deps, creates tofu.apply command and returns it.
	// For non-managed deps, marks succeeded immediately with user config as outputs.
	ScheduleAfterInput(ctx context.Context, tx TxRepos, deployment *domain.Deployment, env *domain.Environment, depName string) (*domain.DependencyDeployment, error)

	// AdvanceOnResult handles a tofu.apply result: marks the dep succeeded/failed,
	// cancels sibling commands on failure, and returns whether all deps are now complete.
	AdvanceOnResult(ctx context.Context, tx TxRepos, deploymentID uuid.UUID, cfg *domain.DependencyDeployment, success bool, output json.RawMessage) (allComplete bool, err error)

	// BuildContexts unmarshals raw dependency outputs into a name→values map for
	// helm value generation. rawOutputs comes from GetDepOutputsByDeployment and
	// includes both managed (tofu) and non-managed (user_config) deps.
	BuildContexts(rawOutputs map[string]json.RawMessage) (map[string]map[string]any, error)

	// Validate checks that all declared dep types are known.
	Validate(ctx context.Context, deps map[string]serviceconfig.DependencyDeclaration) error
}

// AgentConnectionService manages the event-driven protocol for connected agents.
type AgentConnectionService interface {
	// NewSession creates a session for a connected agent. The session handles
	// event bus subscriptions and command dispatch protocol.
	NewSession(clusterID uuid.UUID, log *slog.Logger) AgentSession
}

// AgentSession represents an active agent connection's event protocol.
type AgentSession interface {
	// Start subscribes to events and returns channels for the handler to consume.
	// Returns (dispatchCh, wakeCh, error). Caller must call Close() when done.
	Start(ctx context.Context) (<-chan *domain.Command, <-chan struct{}, error)

	// RequestNext signals agent readiness and waits for a command dispatch.
	// Returns the command to send, or nil if no command is available within timeout.
	RequestNext(ctx context.Context) *domain.Command

	// OnAck publishes CommandStarted when agent acknowledges a command.
	OnAck(ctx context.Context, deploymentID uuid.UUID)

	// OnResult publishes CommandResult.
	OnResult(ctx context.Context, result events.CommandResult)

	// OnLog publishes CommandLog for streaming.
	OnLog(ctx context.Context, commandID uuid.UUID, line string)

	// Close publishes AgentDisconnected and unsubscribes from events.
	Close(inFlightCommandID uuid.UUID)
}
