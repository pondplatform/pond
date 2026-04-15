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
	// ListByService returns deployments for a service, optionally filtered by environment and status.
	ListByService(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *domain.DeploymentStatus, limit int, cursor string) ([]domain.Deployment, error)
	// Cancel cancels an in-progress deployment. Returns an error if the deployment is already in a terminal state.
	Cancel(ctx context.Context, deploymentID uuid.UUID) error
	// ProvideUserInput updates a dependency config with user-provided input and
	// publishes a UserInputProvided event to trigger scheduling.
	ProvideUserInput(ctx context.Context, deploymentID uuid.UUID, depName string, input UserInputRequest) error
	// Start registers subscriptions on the event bus (agent.ready,
	// command.started, command.results, command.logs, agent.disconnected,
	// user_input.provided) and drives the deployment state machine in response.
	// Blocks until ctx is cancelled.
	Start(ctx context.Context)
}

type UserInputRequest struct {
	Managed        bool           `json:"managed"`
	ProviderInputs map[string]any `json:"provider_inputs"`
	UserConfig     map[string]any `json:"user_config"`
}

type SubmitRequest struct {
	ProjectID         uuid.UUID
	EnvironmentName   string
	OverridableConfig domain.OverridableConfig
	ImageTag          string
	TriggeredBy       string
	CreateIfNotExists bool
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
	Validate(ctx context.Context, deps map[string]domain.DependencyDeclaration) error
}
