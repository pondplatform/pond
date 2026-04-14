package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

// DeploymentInfoStore manages all deployment, command, and dependency config persistence.
type DeploymentInfoStore interface {
	// Deployment operations
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	ListByService(ctx context.Context, serviceID uuid.UUID) ([]domain.Deployment, error)
	Create(ctx context.Context, d *domain.Deployment) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.DeploymentStatus, completedAt *time.Time) error
	SetHelmCommandID(ctx context.Context, id uuid.UUID, cmdID uuid.UUID) error
	GetByHelmCommandID(ctx context.Context, cmdID uuid.UUID) (*domain.Deployment, error)

	// Command operations (pure CRUD)
	CreateCommand(ctx context.Context, cmd *domain.Command) error
	GetCommand(ctx context.Context, id uuid.UUID) (*domain.Command, error)
	UpdateCommand(ctx context.Context, cmd *domain.Command) error
	ListQueuedCommandsByCluster(ctx context.Context, clusterID uuid.UUID) ([]*domain.Command, error)
	UpdateCommandsByDeployment(ctx context.Context, deploymentID uuid.UUID, fromStatus, toStatus domain.CommandStatus) error

	// Command log operations
	AppendLog(ctx context.Context, commandID uuid.UUID, line string) error

	// Dependency config operations (dependency_deployments table)
	CreateDepConfig(ctx context.Context, cfg *domain.DeploymentDependencyConfig) error
	GetDepConfig(ctx context.Context, deploymentID uuid.UUID, depName string) (*domain.DeploymentDependencyConfig, error)
	// GetDepConfigByCommandID returns the deployment ID and dep config for the given
	// command. Returns (uuid.Nil, nil, nil) when not found.
	GetDepConfigByCommandID(ctx context.Context, commandID uuid.UUID) (deploymentID uuid.UUID, cfg *domain.DeploymentDependencyConfig, err error)
	MarkDepConfigSucceeded(ctx context.Context, deploymentID uuid.UUID, depName string, output json.RawMessage) error
	MarkDepConfigFailed(ctx context.Context, deploymentID uuid.UUID, depName string) error
	AllDepConfigsComplete(ctx context.Context, deploymentID uuid.UUID) (allSucceeded bool, anyFailed bool, err error)
	GetDepOutputsByDeployment(ctx context.Context, deploymentID uuid.UUID) (map[string]json.RawMessage, error)
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

