package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/store"
)

type DeploymentService interface {
	Submit(ctx context.Context, req SubmitRequest) (*domain.Deployment, error)
	GetStatus(ctx context.Context, deploymentID uuid.UUID) (*domain.Deployment, error)
	Validate(ctx context.Context, req SubmitRequest) (*ValidationResult, error)
	// MarkRunning transitions a deployment from pending → running when the agent
	// confirms it has begun executing the command.
	MarkRunning(ctx context.Context, deploymentID uuid.UUID) error
	// Start subscribes to the command_results event topic and advances the
	// deployment state machine for each result. Blocks until ctx is cancelled.
	Start(ctx context.Context)
}

type SubmitRequest struct {
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	ServiceConfig domain.ServiceConfig
	ImageTag      string
	TriggeredBy   string
}

type ValidationResult struct {
	Valid    bool
	Errors   []domain.ValidationError
	Warnings []domain.ValidationWarning
}

// TxRepos holds the writable repositories needed within a single transaction.
type TxRepos struct {
	Deployments store.DeploymentRepository
	DepRequests store.DependencyDeploymentRequestRepository
	Commands    store.CommandRepository
}

// Transactor executes fn inside a database transaction, rolling back on error.
type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx TxRepos) error) error
}
