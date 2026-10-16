// Package testutil provides hand-rolled mock implementations of domain
// interfaces for use in unit tests across the project.
package testutil

import (
	"context"
	"time"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
)

// --- store.DeploymentInfoStore ---

type MockDeploymentInfoStore struct {
	GetByIDFn                    func(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	LockDeploymentFn             func(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	ListByServiceFilteredFn      func(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *api.DeploymentStatus, limit int, cursor string) ([]domain.Deployment, error)
	CreateFn                     func(ctx context.Context, d *domain.Deployment) error
	SaveDeploymentFn             func(ctx context.Context, d *domain.Deployment) error
	SetExportsFn                 func(ctx context.Context, id uuid.UUID, exports map[string]string) error
	CreateCommandFn              func(ctx context.Context, cmd *domain.Command) error
	GetCommandFn                 func(ctx context.Context, id uuid.UUID) (*domain.Command, error)
	UpdateCommandFn              func(ctx context.Context, cmd *domain.Command) error
	ListCommandsByDeploymentFn   func(ctx context.Context, deploymentID uuid.UUID) ([]*domain.Command, error)
	ClaimNextCommandFn           func(ctx context.Context, clusterID uuid.UUID, at time.Time) (*domain.Command, error)
	DeploymentStateOfCommandFn   func(ctx context.Context, commandID uuid.UUID) (domain.DeploymentState, error)
	UpdateCommandsByDeploymentFn func(ctx context.Context, deploymentID uuid.UUID, to domain.CommandStatus, from ...domain.CommandStatus) error
	AppendLogFn                  func(ctx context.Context, clusterID, commandID uuid.UUID, line string) error
	GetCommandLogsFn             func(ctx context.Context, commandID uuid.UUID) ([]domain.CommandLog, error)
	CreateDepConfigFn            func(ctx context.Context, cfg *domain.DependencyDeployment) error
	ListDepConfigsFn             func(ctx context.Context, deploymentID uuid.UUID) ([]domain.DependencyDeployment, error)
}

func (m *MockDeploymentInfoStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}
func (m *MockDeploymentInfoStore) LockDeployment(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	if m.LockDeploymentFn != nil {
		return m.LockDeploymentFn(ctx, id)
	}
	return nil, nil
}
func (m *MockDeploymentInfoStore) ListByServiceFiltered(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *api.DeploymentStatus, limit int, cursor string) ([]domain.Deployment, error) {
	if m.ListByServiceFilteredFn != nil {
		return m.ListByServiceFilteredFn(ctx, serviceID, environmentID, status, limit, cursor)
	}
	return nil, nil
}
func (m *MockDeploymentInfoStore) Create(ctx context.Context, d *domain.Deployment) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, d)
	}
	return nil
}
func (m *MockDeploymentInfoStore) SaveDeployment(ctx context.Context, d *domain.Deployment) error {
	if m.SaveDeploymentFn != nil {
		return m.SaveDeploymentFn(ctx, d)
	}
	return nil
}
func (m *MockDeploymentInfoStore) SetExports(ctx context.Context, id uuid.UUID, exports map[string]string) error {
	if m.SetExportsFn != nil {
		return m.SetExportsFn(ctx, id, exports)
	}
	return nil
}
func (m *MockDeploymentInfoStore) CreateCommand(ctx context.Context, cmd *domain.Command) error {
	if m.CreateCommandFn != nil {
		return m.CreateCommandFn(ctx, cmd)
	}
	return nil
}
func (m *MockDeploymentInfoStore) GetCommand(ctx context.Context, id uuid.UUID) (*domain.Command, error) {
	if m.GetCommandFn != nil {
		return m.GetCommandFn(ctx, id)
	}
	return nil, nil
}
func (m *MockDeploymentInfoStore) UpdateCommand(ctx context.Context, cmd *domain.Command) error {
	if m.UpdateCommandFn != nil {
		return m.UpdateCommandFn(ctx, cmd)
	}
	return nil
}
func (m *MockDeploymentInfoStore) ListCommandsByDeployment(ctx context.Context, deploymentID uuid.UUID) ([]*domain.Command, error) {
	if m.ListCommandsByDeploymentFn != nil {
		return m.ListCommandsByDeploymentFn(ctx, deploymentID)
	}
	return nil, nil
}
func (m *MockDeploymentInfoStore) ClaimNextCommand(ctx context.Context, clusterID uuid.UUID, at time.Time) (*domain.Command, error) {
	if m.ClaimNextCommandFn != nil {
		return m.ClaimNextCommandFn(ctx, clusterID, at)
	}
	return nil, nil
}
func (m *MockDeploymentInfoStore) DeploymentStateOfCommand(ctx context.Context, commandID uuid.UUID) (domain.DeploymentState, error) {
	if m.DeploymentStateOfCommandFn != nil {
		return m.DeploymentStateOfCommandFn(ctx, commandID)
	}
	return "", nil
}
func (m *MockDeploymentInfoStore) UpdateCommandsByDeployment(ctx context.Context, deploymentID uuid.UUID, to domain.CommandStatus, from ...domain.CommandStatus) error {
	if m.UpdateCommandsByDeploymentFn != nil {
		return m.UpdateCommandsByDeploymentFn(ctx, deploymentID, to, from...)
	}
	return nil
}
func (m *MockDeploymentInfoStore) AppendLog(ctx context.Context, clusterID, commandID uuid.UUID, line string) error {
	if m.AppendLogFn != nil {
		return m.AppendLogFn(ctx, clusterID, commandID, line)
	}
	return nil
}
func (m *MockDeploymentInfoStore) GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]domain.CommandLog, error) {
	if m.GetCommandLogsFn != nil {
		return m.GetCommandLogsFn(ctx, commandID)
	}
	return nil, nil
}
func (m *MockDeploymentInfoStore) CreateDepConfig(ctx context.Context, cfg *domain.DependencyDeployment) error {
	if m.CreateDepConfigFn != nil {
		return m.CreateDepConfigFn(ctx, cfg)
	}
	return nil
}
func (m *MockDeploymentInfoStore) ListDepConfigs(ctx context.Context, deploymentID uuid.UUID) ([]domain.DependencyDeployment, error) {
	if m.ListDepConfigsFn != nil {
		return m.ListDepConfigsFn(ctx, deploymentID)
	}
	return nil, nil
}

// --- store.ServiceRepository ---

type MockServiceRepository struct {
	GetByIDFn       func(ctx context.Context, id uuid.UUID) (*domain.Service, error)
	GetByNameFn     func(ctx context.Context, projectID uuid.UUID, name string) (*domain.Service, error)
	ListByProjectFn func(ctx context.Context, projectID uuid.UUID) ([]domain.Service, error)
	CreateFn        func(ctx context.Context, svc *domain.Service) error

	SetCurrentDeploymentFn func(ctx context.Context, serviceID, deploymentID uuid.UUID) error
	SetRunningDeploymentFn func(ctx context.Context, serviceID, deploymentID uuid.UUID) error
}

func (m *MockServiceRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Service, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}
func (m *MockServiceRepository) GetByName(ctx context.Context, projectID uuid.UUID, name string) (*domain.Service, error) {
	if m.GetByNameFn != nil {
		return m.GetByNameFn(ctx, projectID, name)
	}
	return nil, nil
}
func (m *MockServiceRepository) ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Service, error) {
	if m.ListByProjectFn != nil {
		return m.ListByProjectFn(ctx, projectID)
	}
	return nil, nil
}
func (m *MockServiceRepository) Create(ctx context.Context, svc *domain.Service) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, svc)
	}
	return nil
}
func (m *MockServiceRepository) SetCurrentDeployment(ctx context.Context, serviceID, deploymentID uuid.UUID) error {
	if m.SetCurrentDeploymentFn != nil {
		return m.SetCurrentDeploymentFn(ctx, serviceID, deploymentID)
	}
	return nil
}
func (m *MockServiceRepository) SetRunningDeployment(ctx context.Context, serviceID, deploymentID uuid.UUID) error {
	if m.SetRunningDeploymentFn != nil {
		return m.SetRunningDeploymentFn(ctx, serviceID, deploymentID)
	}
	return nil
}

// --- store.EnvironmentRepository ---

type MockEnvironmentRepository struct {
	GetByIDFn       func(ctx context.Context, id uuid.UUID) (*domain.Environment, error)
	GetByNameFn     func(ctx context.Context, projectID uuid.UUID, name string) (*domain.Environment, error)
	ListByProjectFn func(ctx context.Context, projectID uuid.UUID) ([]domain.Environment, error)
	CreateFn        func(ctx context.Context, env *domain.Environment) error
	UpdateFn        func(ctx context.Context, env *domain.Environment) error
}

func (m *MockEnvironmentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Environment, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}
func (m *MockEnvironmentRepository) GetByName(ctx context.Context, projectID uuid.UUID, name string) (*domain.Environment, error) {
	if m.GetByNameFn != nil {
		return m.GetByNameFn(ctx, projectID, name)
	}
	return nil, nil
}
func (m *MockEnvironmentRepository) ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Environment, error) {
	if m.ListByProjectFn != nil {
		return m.ListByProjectFn(ctx, projectID)
	}
	return nil, nil
}
func (m *MockEnvironmentRepository) Create(ctx context.Context, env *domain.Environment) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, env)
	}
	return nil
}
func (m *MockEnvironmentRepository) Update(ctx context.Context, env *domain.Environment) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, env)
	}
	return nil
}

// --- store.ClusterRepository ---

type MockClusterRepository struct {
	GetByIDFn         func(ctx context.Context, id uuid.UUID) (*domain.Cluster, error)
	GetByNameFn       func(ctx context.Context, name string) (*domain.Cluster, error)
	GetByTokenHashFn  func(ctx context.Context, hash string) (*domain.Cluster, error)
	ListFn            func(ctx context.Context) ([]domain.Cluster, error)
	CreateFn          func(ctx context.Context, cluster *domain.Cluster) error
	UpdateLastSeenFn  func(ctx context.Context, id uuid.UUID, lastSeen time.Time) error
	UpdateTokenHashFn func(ctx context.Context, id uuid.UUID, tokenHash string) error
}

func (m *MockClusterRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Cluster, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}
func (m *MockClusterRepository) GetByName(ctx context.Context, name string) (*domain.Cluster, error) {
	if m.GetByNameFn != nil {
		return m.GetByNameFn(ctx, name)
	}
	return nil, nil
}
func (m *MockClusterRepository) GetByTokenHash(ctx context.Context, hash string) (*domain.Cluster, error) {
	if m.GetByTokenHashFn != nil {
		return m.GetByTokenHashFn(ctx, hash)
	}
	return nil, nil
}
func (m *MockClusterRepository) List(ctx context.Context) ([]domain.Cluster, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx)
	}
	return nil, nil
}
func (m *MockClusterRepository) Create(ctx context.Context, cluster *domain.Cluster) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, cluster)
	}
	return nil
}
func (m *MockClusterRepository) UpdateLastSeen(ctx context.Context, id uuid.UUID, lastSeen time.Time) error {
	if m.UpdateLastSeenFn != nil {
		return m.UpdateLastSeenFn(ctx, id, lastSeen)
	}
	return nil
}
func (m *MockClusterRepository) UpdateTokenHash(ctx context.Context, id uuid.UUID, tokenHash string) error {
	if m.UpdateTokenHashFn != nil {
		return m.UpdateTokenHashFn(ctx, id, tokenHash)
	}
	return nil
}

// Compile-time interface checks.
var (
	_ store.DeploymentInfoStore   = (*MockDeploymentInfoStore)(nil)
	_ store.ServiceRepository     = (*MockServiceRepository)(nil)
	_ store.EnvironmentRepository = (*MockEnvironmentRepository)(nil)
	_ store.ClusterRepository     = (*MockClusterRepository)(nil)
)
