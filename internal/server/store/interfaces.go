package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

// DeploymentRepository manages deployment persistence.
type DeploymentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	ListByService(ctx context.Context, serviceID uuid.UUID) ([]domain.Deployment, error)
	Create(ctx context.Context, d *domain.Deployment) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.DeploymentStatus, completedAt *time.Time) error
	SetHelmCommandID(ctx context.Context, id uuid.UUID, cmdID uuid.UUID) error
	GetByHelmCommandID(ctx context.Context, cmdID uuid.UUID) (*domain.Deployment, error)
}

// ServiceRepository manages service persistence.
type ServiceRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Service, error)
	GetByName(ctx context.Context, projectID uuid.UUID, name string) (*domain.Service, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Service, error)
	Create(ctx context.Context, svc *domain.Service) error
}

// EnvironmentRepository manages environment persistence.
type EnvironmentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Environment, error)
	GetByName(ctx context.Context, projectID uuid.UUID, name string) (*domain.Environment, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Environment, error)
	Create(ctx context.Context, env *domain.Environment) error
}

// OrganizationRepository manages organization persistence.
type OrganizationRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Organization, error)
	GetByName(ctx context.Context, name string) (*domain.Organization, error)
	Create(ctx context.Context, org *domain.Organization) error
}

// ProjectRepository manages project persistence.
type ProjectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Project, error)
	GetByName(ctx context.Context, orgID uuid.UUID, name string) (*domain.Project, error)
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]domain.Project, error)
	Create(ctx context.Context, project *domain.Project) error
	SetRootEnvironment(ctx context.Context, projectID, envID uuid.UUID) error
}

// ClusterRepository manages cluster persistence.
type ClusterRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Cluster, error)
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]domain.Cluster, error)
	Create(ctx context.Context, cluster *domain.Cluster) error
	UpdateLastSeen(ctx context.Context, id uuid.UUID, t time.Time) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*domain.Cluster, error)
}

// DependencyConfigRepository manages dependency configuration persistence.
type DependencyConfigRepository interface {
	Get(ctx context.Context, serviceID, envID uuid.UUID, depName string) (*domain.DependencyConfig, error)
	Set(ctx context.Context, cfg *domain.DependencyConfig) error
	ListByServiceAndEnv(ctx context.Context, serviceID, envID uuid.UUID) ([]domain.DependencyConfig, error)
}

// ResolvedContextRepository manages resolved dependency context persistence.
type ResolvedContextRepository interface {
	Get(ctx context.Context, serviceID, envID uuid.UUID, depName string) (*domain.ResolvedContext, error)
	Set(ctx context.Context, rc *domain.ResolvedContext) error
}

// CommandRepository manages the unified command lifecycle in the commands table.
type CommandRepository interface {
	// Enqueue inserts a command with status=queued.
	Enqueue(ctx context.Context, clusterID uuid.UUID, cmd *domain.Command) error
	// Dequeue atomically transitions the next queued command to dispatched
	// via SELECT FOR UPDATE SKIP LOCKED. Returns nil if the queue is empty.
	Dequeue(ctx context.Context, clusterID uuid.UUID) (*domain.Command, error)
	// MarkSucceeded transitions a command to succeeded and stores its output.
	MarkSucceeded(ctx context.Context, commandID uuid.UUID, output json.RawMessage) error
	// MarkFailed transitions a command to failed and stores the error message.
	MarkFailed(ctx context.Context, commandID uuid.UUID, errMsg string) error
	// Requeue transitions a dispatched command back to queued (agent disconnect
	// recovery). Guard: only applies when status = 'dispatched'.
	Requeue(ctx context.Context, commandID uuid.UUID) error
	// CancelDeployment transitions all queued commands for a deployment to
	// cancelled. Already-dispatched commands are unaffected.
	CancelDeployment(ctx context.Context, deploymentID uuid.UUID) error
}

// CommandLogRepository persists log lines streamed by agents during command
// execution.
type CommandLogRepository interface {
	Append(ctx context.Context, commandID uuid.UUID, line string) error
}

// DependencyDeploymentRequestRepository persists dependency requests.
type DependencyDeploymentRequestRepository interface {
	Create(ctx context.Context, req *domain.DependencyDeploymentRequest) error
	GetByCommandID(ctx context.Context, commandID uuid.UUID) (*domain.DependencyDeploymentRequest, error)
	MarkSucceeded(ctx context.Context, commandID uuid.UUID, output json.RawMessage) error
	MarkFailed(ctx context.Context, commandID uuid.UUID) error
	AllComplete(ctx context.Context, deploymentID uuid.UUID) (allSucceeded bool, anyFailed bool, err error)
	GetOutputsByDeployment(ctx context.Context, deploymentID uuid.UUID) (map[string]json.RawMessage, error)
}
