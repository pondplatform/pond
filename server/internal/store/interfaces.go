package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

// DeploymentInfoStore manages all deployment, command, and dependency config persistence.
type DeploymentInfoStore interface {
	// Deployment operations
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	// LockDeployment is GetByID with SELECT … FOR UPDATE; call it inside a
	// transaction to serialise every change to one deployment.
	LockDeployment(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	ListByServiceFiltered(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *api.DeploymentStatus, limit int, cursor string) ([]domain.Deployment, error)
	// ListLatestByProject returns the latest deployment of every service of
	// the project in every environment it was deployed to.
	ListLatestByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Deployment, error)
	// ListUnfinishedByService returns the service's deployments that are in
	// progress, or whose agent is still running one of their commands.
	ListUnfinishedByService(ctx context.Context, serviceID uuid.UUID) ([]domain.Deployment, error)
	Create(ctx context.Context, d *domain.Deployment) error
	// SaveDeployment writes status, error and completed_at.
	SaveDeployment(ctx context.Context, d *domain.Deployment) error
	SetExports(ctx context.Context, id uuid.UUID, exports map[string]string) error

	// Command operations
	CreateCommand(ctx context.Context, cmd *domain.Command) error
	GetCommand(ctx context.Context, id uuid.UUID) (*domain.Command, error)
	// UpdateCommand writes status, output, error and updated_at.
	UpdateCommand(ctx context.Context, cmd *domain.Command) error
	// ListCommandsByDeployment returns the deployment's commands, oldest first.
	ListCommandsByDeployment(ctx context.Context, deploymentID uuid.UUID) ([]*domain.Command, error)
	// ListCommandsByServiceEnv returns the commands of the given types of all
	// the service's deployments in the environment, oldest first.
	ListCommandsByServiceEnv(ctx context.Context, serviceID, environmentID uuid.UUID, types ...string) ([]*domain.Command, error)
	// ClaimNextCommand dispatches the cluster's oldest queued command, or
	// returns nil when there is none.
	ClaimNextCommand(ctx context.Context, clusterID uuid.UUID, at time.Time) (*domain.Command, error)
	// DeploymentStateOfCommand returns the stored state of the command's
	// deployment.
	DeploymentStateOfCommand(ctx context.Context, commandID uuid.UUID) (domain.DeploymentState, error)
	// UpdateCommandsByDeployment moves the deployment's commands in any of the
	// from statuses to the to status.
	UpdateCommandsByDeployment(ctx context.Context, deploymentID uuid.UUID, to domain.CommandStatus, from ...domain.CommandStatus) error

	// Command log operations
	// AppendLog stores a log line of a command that runs on the cluster; a
	// line for another cluster's command is dropped.
	AppendLog(ctx context.Context, clusterID, commandID uuid.UUID, line string) error
	GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]domain.CommandLog, error)

	// Dependency config operations (dependency_deployments table)
	CreateDepConfig(ctx context.Context, cfg *domain.DependencyDeployment) error
	// ListDepConfigs returns all dependency configs for the given deployment, sorted by name.
	ListDepConfigs(ctx context.Context, deploymentID uuid.UUID) ([]domain.DependencyDeployment, error)
}

// ServiceRepository manages service persistence.
type ServiceRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Service, error)
	GetByName(ctx context.Context, projectID uuid.UUID, name string) (*domain.Service, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Service, error)
	// Lock takes the service's row lock, serialising deploys and deletes of
	// the service.
	Lock(ctx context.Context, id uuid.UUID) error
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
