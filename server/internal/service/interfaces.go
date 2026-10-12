package service

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

type DeploymentDetail struct {
	Deployment   *db.Deployment
	Dependencies []db.DependencyDeployment
	Commands     []*db.Command
}

type DeploymentService interface {
	Submit(ctx context.Context, req api.SubmitRequest) (*db.Deployment, error)
	GetStatus(ctx context.Context, deploymentID uuid.UUID) (*DeploymentDetail, error)
	Validate(ctx context.Context, req api.SubmitRequest) (*ValidationResult, error)
	ListByService(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *db.DeploymentStatus, limit int, cursor string) ([]db.Deployment, error)
	Cancel(ctx context.Context, deploymentID uuid.UUID) error
	ConfigureDeployment(ctx context.Context, deploymentID uuid.UUID, inputs map[string]api.DependencyInput) error
	GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]db.CommandLog, error)
}

type deploymentStateMachine interface {
	InitializeDeployment(ctx context.Context, deployment db.Deployment, dependencies []db.DependencyDeployment) error
	AdvanceOnCommandResult(ctx context.Context, commandResult events.CommandResult) error
}

type dependencyService interface {
	Validate(ctx context.Context, deps map[string]serviceconfig.DependencyDeclaration) error
	ScheduleCommand(ctx context.Context, deployment db.Deployment, dependency db.DependencyDeployment) error
}

type AgentConnectionService interface {
	// NewSession creates a session for a connected agent. The session handles
	// event bus subscriptions and command dispatch protocol.
	NewSession(clusterID uuid.UUID, log *slog.Logger) AgentSession
}

type AgentSession interface {
	// Start subscribes to events and returns channels for the handler to consume.
	// Returns (dispatchCh, wakeCh, error). Caller must call Close() when done.
	Start(ctx context.Context) (<-chan *db.Command, <-chan struct{}, error)

	// RequestNext signals agent readiness and waits for a command dispatch.
	// Returns the command to send, or nil if no command is available within timeout.
	RequestNext(ctx context.Context) *db.Command

	// OnAck publishes CommandStarted when agent acknowledges a command.
	OnAck(ctx context.Context, commandID uuid.UUID)

	// OnResult publishes CommandResult.
	OnResult(ctx context.Context, result events.CommandResult)

	// OnLog publishes CommandLog for streaming.
	OnLog(ctx context.Context, commandID uuid.UUID, line string)

	// Close publishes AgentDisconnected and unsubscribes from events.
	Close()
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
