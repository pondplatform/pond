package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
)

// DeploymentInfoStore manages all deployment, command, and dependency config persistence.
type DeploymentInfoStore interface {
	// Deployment operations
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	// LockDeployment is GetByID with SELECT … FOR UPDATE; call it inside a
	// transaction to serialise every change to one deployment.
	LockDeployment(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	ListByServiceFiltered(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *domain.DeploymentStatus, limit int, cursor string) ([]domain.Deployment, error)
	Create(ctx context.Context, d *domain.Deployment) error
	// SaveDeployment writes status, error, completed_at and helm_command_id.
	SaveDeployment(ctx context.Context, d *domain.Deployment) error
	// ListStalledDeployments returns up to limit stalled deployments, oldest first.
	ListStalledDeployments(ctx context.Context, limit int) ([]StalledDeployment, error)

	// Command operations
	CreateCommand(ctx context.Context, cmd *domain.Command) error
	GetCommand(ctx context.Context, id uuid.UUID) (*domain.Command, error)
	UpdateCommand(ctx context.Context, cmd *domain.Command) error
	ListCommandsByDeployment(ctx context.Context, deploymentID uuid.UUID) ([]*domain.Command, error)
	ListQueuedCommandsByCluster(ctx context.Context, clusterID uuid.UUID) ([]*domain.Command, error)
	ListDispatchedCommandsByCluster(ctx context.Context, clusterID uuid.UUID) ([]*domain.Command, error)
	// ListDispatchedCommandsBefore returns up to limit commands dispatched
	// before the given time and still without a result, oldest first.
	ListDispatchedCommandsBefore(ctx context.Context, before time.Time, limit int) ([]*domain.Command, error)
	// UpdateCommandsByDeployment moves the deployment's commands in any of the
	// from statuses to the to status.
	UpdateCommandsByDeployment(ctx context.Context, deploymentID uuid.UUID, to domain.CommandStatus, from ...domain.CommandStatus) error

	// Command log operations
	AppendLog(ctx context.Context, commandID uuid.UUID, line string) error
	GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]domain.CommandLog, error)

	// Dependency config operations (dependency_deployments table)
	CreateDepConfig(ctx context.Context, cfg *domain.DependencyDeployment) error
	// ListDepConfigs returns all dependency configs for the given deployment, sorted by name.
	ListDepConfigs(ctx context.Context, deploymentID uuid.UUID) ([]domain.DependencyDeployment, error)
	// UpdateDepConfig persists all mutable fields of a dependency deployment row.
	UpdateDepConfig(ctx context.Context, cfg *domain.DependencyDeployment) error
}

// ServiceRepository manages service persistence.
type ServiceRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Service, error)
	GetByName(ctx context.Context, projectID uuid.UUID, name string) (*domain.Service, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Service, error)
	Create(ctx context.Context, svc *domain.Service) error
	// SetCurrentDeployment records the service's most recently submitted deployment.
	SetCurrentDeployment(ctx context.Context, serviceID, deploymentID uuid.UUID) error
	// SetRunningDeployment records the service's most recently succeeded deployment.
	SetRunningDeployment(ctx context.Context, serviceID, deploymentID uuid.UUID) error
}

// EnvironmentRepository manages environment persistence.
type EnvironmentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Environment, error)
	GetByName(ctx context.Context, projectID uuid.UUID, name string) (*domain.Environment, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Environment, error)
	Create(ctx context.Context, env *domain.Environment) error
	Update(ctx context.Context, env *domain.Environment) error
}

// ProjectRepository manages project persistence.
type ProjectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Project, error)
	GetByName(ctx context.Context, name string) (*domain.Project, error)
	List(ctx context.Context) ([]domain.Project, error)
	Create(ctx context.Context, project *domain.Project) error
	SetRootEnvironment(ctx context.Context, projectID, envID uuid.UUID) error
}

// ClusterRepository manages cluster persistence.
type ClusterRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Cluster, error)
	GetByName(ctx context.Context, name string) (*domain.Cluster, error)
	List(ctx context.Context) ([]domain.Cluster, error)
	Create(ctx context.Context, cluster *domain.Cluster) error
	UpdateLastSeen(ctx context.Context, id uuid.UUID, t time.Time) error
	UpdateTokenHash(ctx context.Context, id uuid.UUID, tokenHash string) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*domain.Cluster, error)
}
