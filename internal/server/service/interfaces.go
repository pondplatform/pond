package service

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/store"
)

type DeploymentService interface {
	Submit(ctx context.Context, req SubmitRequest) (*domain.Deployment, error)
	GetStatus(ctx context.Context, deploymentID uuid.UUID) (*domain.Deployment, error)
	Validate(ctx context.Context, req SubmitRequest) (*ValidationResult, error)
	// Start registers subscriptions on the event bus (agent_ready,
	// command_started, command_results, command_logs, agent_disconnected)
	// and drives the deployment state machine in response. Blocks until ctx
	// is cancelled.
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
	DeploymentInfo store.DeploymentInfoStore
}

// Transactor executes fn inside a database transaction, rolling back on error.
type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx TxRepos) error) error
}

// PendingDep groups a queued tofu.apply command with its dependency config row.
type PendingDep struct {
	Cmd    *domain.Command
	DepCfg *domain.DeploymentDependencyConfig
}

// DependencyService handles dependency command scheduling and context resolution.
type DependencyService interface {
	// ScheduleCommands queues tofu.apply commands for all managed deps in the
	// deployment and creates DependencyDeploymentRequest rows inside tx.
	ScheduleCommands(ctx context.Context, tx TxRepos, dep *domain.Deployment, clusterID uuid.UUID) ([]PendingDep, error)

	// BuildContexts unmarshals raw dependency outputs into a name→values map for
	// helm value generation. rawOutputs comes from GetDepOutputsByDeployment and
	// includes both managed (tofu) and non-managed (user_config) deps.
	BuildContexts(rawOutputs map[string]json.RawMessage) (map[string]map[string]any, error)

	// Validate checks that all declared dep types are known and each dep is
	// configured for the given (service, environment) pair.
	Validate(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) error
}
