// Package testutil provides hand-rolled mock implementations of domain
// interfaces for use in unit tests across the project.
package testutil

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/events"
	"github.com/pondplatform/pond/internal/server/helmgen"
	"github.com/pondplatform/pond/internal/server/store"
)

// --- store.DeploymentRepository ---

type MockDeploymentRepository struct {
	GetByIDFn            func(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	ListByServiceFn      func(ctx context.Context, serviceID uuid.UUID) ([]domain.Deployment, error)
	CreateFn             func(ctx context.Context, d *domain.Deployment) error
	UpdateStatusFn       func(ctx context.Context, id uuid.UUID, status domain.DeploymentStatus, completedAt *time.Time) error
	SetHelmCommandIDFn   func(ctx context.Context, id uuid.UUID, cmdID uuid.UUID) error
	GetByHelmCommandIDFn func(ctx context.Context, cmdID uuid.UUID) (*domain.Deployment, error)
}

func (m *MockDeploymentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}
func (m *MockDeploymentRepository) ListByService(ctx context.Context, serviceID uuid.UUID) ([]domain.Deployment, error) {
	if m.ListByServiceFn != nil {
		return m.ListByServiceFn(ctx, serviceID)
	}
	return nil, nil
}
func (m *MockDeploymentRepository) Create(ctx context.Context, d *domain.Deployment) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, d)
	}
	return nil
}
func (m *MockDeploymentRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.DeploymentStatus, completedAt *time.Time) error {
	if m.UpdateStatusFn != nil {
		return m.UpdateStatusFn(ctx, id, status, completedAt)
	}
	return nil
}
func (m *MockDeploymentRepository) SetHelmCommandID(ctx context.Context, id uuid.UUID, cmdID uuid.UUID) error {
	if m.SetHelmCommandIDFn != nil {
		return m.SetHelmCommandIDFn(ctx, id, cmdID)
	}
	return nil
}
func (m *MockDeploymentRepository) GetByHelmCommandID(ctx context.Context, cmdID uuid.UUID) (*domain.Deployment, error) {
	if m.GetByHelmCommandIDFn != nil {
		return m.GetByHelmCommandIDFn(ctx, cmdID)
	}
	return nil, nil
}

// --- store.ServiceRepository ---

type MockServiceRepository struct {
	GetByIDFn       func(ctx context.Context, id uuid.UUID) (*domain.Service, error)
	GetByNameFn     func(ctx context.Context, projectID uuid.UUID, name string) (*domain.Service, error)
	ListByProjectFn func(ctx context.Context, projectID uuid.UUID) ([]domain.Service, error)
	CreateFn        func(ctx context.Context, svc *domain.Service) error
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

// --- store.EnvironmentRepository ---

type MockEnvironmentRepository struct {
	GetByIDFn       func(ctx context.Context, id uuid.UUID) (*domain.Environment, error)
	GetByNameFn     func(ctx context.Context, projectID uuid.UUID, name string) (*domain.Environment, error)
	ListByProjectFn func(ctx context.Context, projectID uuid.UUID) ([]domain.Environment, error)
	CreateFn        func(ctx context.Context, env *domain.Environment) error
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

// --- store.DependencyConfigRepository ---

type MockDependencyConfigRepository struct {
	GetFn                 func(ctx context.Context, serviceID, envID uuid.UUID, depName string) (*domain.DependencyConfig, error)
	SetFn                 func(ctx context.Context, cfg *domain.DependencyConfig) error
	ListByServiceAndEnvFn func(ctx context.Context, serviceID, envID uuid.UUID) ([]domain.DependencyConfig, error)
}

func (m *MockDependencyConfigRepository) Get(ctx context.Context, serviceID, envID uuid.UUID, depName string) (*domain.DependencyConfig, error) {
	if m.GetFn != nil {
		return m.GetFn(ctx, serviceID, envID, depName)
	}
	return nil, nil
}
func (m *MockDependencyConfigRepository) Set(ctx context.Context, cfg *domain.DependencyConfig) error {
	if m.SetFn != nil {
		return m.SetFn(ctx, cfg)
	}
	return nil
}
func (m *MockDependencyConfigRepository) ListByServiceAndEnv(ctx context.Context, serviceID, envID uuid.UUID) ([]domain.DependencyConfig, error) {
	if m.ListByServiceAndEnvFn != nil {
		return m.ListByServiceAndEnvFn(ctx, serviceID, envID)
	}
	return nil, nil
}

// --- store.DependencyDeploymentRequestRepository ---

type MockDepRequestRepository struct {
	CreateFn                 func(ctx context.Context, req *domain.DependencyDeploymentRequest) error
	GetByCommandIDFn         func(ctx context.Context, commandID uuid.UUID) (*domain.DependencyDeploymentRequest, error)
	MarkSucceededFn          func(ctx context.Context, commandID uuid.UUID, output json.RawMessage) error
	MarkFailedFn             func(ctx context.Context, commandID uuid.UUID) error
	AllCompleteFn            func(ctx context.Context, deploymentID uuid.UUID) (bool, bool, error)
	GetOutputsByDeploymentFn func(ctx context.Context, deploymentID uuid.UUID) (map[string]json.RawMessage, error)
}

func (m *MockDepRequestRepository) Create(ctx context.Context, req *domain.DependencyDeploymentRequest) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, req)
	}
	return nil
}
func (m *MockDepRequestRepository) GetByCommandID(ctx context.Context, commandID uuid.UUID) (*domain.DependencyDeploymentRequest, error) {
	if m.GetByCommandIDFn != nil {
		return m.GetByCommandIDFn(ctx, commandID)
	}
	return nil, nil
}
func (m *MockDepRequestRepository) MarkSucceeded(ctx context.Context, commandID uuid.UUID, output json.RawMessage) error {
	if m.MarkSucceededFn != nil {
		return m.MarkSucceededFn(ctx, commandID, output)
	}
	return nil
}
func (m *MockDepRequestRepository) MarkFailed(ctx context.Context, commandID uuid.UUID) error {
	if m.MarkFailedFn != nil {
		return m.MarkFailedFn(ctx, commandID)
	}
	return nil
}
func (m *MockDepRequestRepository) AllComplete(ctx context.Context, deploymentID uuid.UUID) (bool, bool, error) {
	if m.AllCompleteFn != nil {
		return m.AllCompleteFn(ctx, deploymentID)
	}
	return false, false, nil
}
func (m *MockDepRequestRepository) GetOutputsByDeployment(ctx context.Context, deploymentID uuid.UUID) (map[string]json.RawMessage, error) {
	if m.GetOutputsByDeploymentFn != nil {
		return m.GetOutputsByDeploymentFn(ctx, deploymentID)
	}
	return nil, nil
}

// --- store.CommandRepository ---

type MockCommandRepository struct {
	EnqueueFn          func(ctx context.Context, clusterID uuid.UUID, cmd *domain.Command) error
	DequeueFn          func(ctx context.Context, clusterID uuid.UUID) (*domain.Command, error)
	MarkSucceededFn    func(ctx context.Context, commandID uuid.UUID, output json.RawMessage) error
	MarkFailedFn       func(ctx context.Context, commandID uuid.UUID, errMsg string) error
	RequeueFn          func(ctx context.Context, commandID uuid.UUID) error
	CancelDeploymentFn func(ctx context.Context, deploymentID uuid.UUID) error
}

func (m *MockCommandRepository) Enqueue(ctx context.Context, clusterID uuid.UUID, cmd *domain.Command) error {
	if m.EnqueueFn != nil {
		return m.EnqueueFn(ctx, clusterID, cmd)
	}
	return nil
}
func (m *MockCommandRepository) Dequeue(ctx context.Context, clusterID uuid.UUID) (*domain.Command, error) {
	if m.DequeueFn != nil {
		return m.DequeueFn(ctx, clusterID)
	}
	return nil, nil
}
func (m *MockCommandRepository) MarkSucceeded(ctx context.Context, commandID uuid.UUID, output json.RawMessage) error {
	if m.MarkSucceededFn != nil {
		return m.MarkSucceededFn(ctx, commandID, output)
	}
	return nil
}
func (m *MockCommandRepository) MarkFailed(ctx context.Context, commandID uuid.UUID, errMsg string) error {
	if m.MarkFailedFn != nil {
		return m.MarkFailedFn(ctx, commandID, errMsg)
	}
	return nil
}
func (m *MockCommandRepository) Requeue(ctx context.Context, commandID uuid.UUID) error {
	if m.RequeueFn != nil {
		return m.RequeueFn(ctx, commandID)
	}
	return nil
}
func (m *MockCommandRepository) CancelDeployment(ctx context.Context, deploymentID uuid.UUID) error {
	if m.CancelDeploymentFn != nil {
		return m.CancelDeploymentFn(ctx, deploymentID)
	}
	return nil
}

// --- store.CommandLogRepository ---

type MockCommandLogRepository struct {
	AppendFn func(ctx context.Context, commandID uuid.UUID, line string) error
}

func (m *MockCommandLogRepository) Append(ctx context.Context, commandID uuid.UUID, line string) error {
	if m.AppendFn != nil {
		return m.AppendFn(ctx, commandID, line)
	}
	return nil
}

// --- store.ClusterRepository ---

type MockClusterRepository struct {
	GetByIDFn          func(ctx context.Context, id uuid.UUID) (*domain.Cluster, error)
	GetByTokenHashFn   func(ctx context.Context, hash string) (*domain.Cluster, error)
	ListByOrganizationFn func(ctx context.Context, orgID uuid.UUID) ([]domain.Cluster, error)
	CreateFn           func(ctx context.Context, cluster *domain.Cluster) error
	UpdateLastSeenFn   func(ctx context.Context, id uuid.UUID, lastSeen time.Time) error
}

func (m *MockClusterRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Cluster, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}
func (m *MockClusterRepository) GetByTokenHash(ctx context.Context, hash string) (*domain.Cluster, error) {
	if m.GetByTokenHashFn != nil {
		return m.GetByTokenHashFn(ctx, hash)
	}
	return nil, nil
}
func (m *MockClusterRepository) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]domain.Cluster, error) {
	if m.ListByOrganizationFn != nil {
		return m.ListByOrganizationFn(ctx, orgID)
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

// --- events.Bus ---

type MockBus struct {
	SubscribeFn func(topic string, handler events.Handler) func()
	PublishFn   func(ctx context.Context, topic string, v any)
}

func (m *MockBus) Subscribe(topic string, handler events.Handler) func() {
	if m.SubscribeFn != nil {
		return m.SubscribeFn(topic, handler)
	}
	return func() {}
}
func (m *MockBus) Publish(ctx context.Context, topic string, v any) {
	if m.PublishFn != nil {
		m.PublishFn(ctx, topic, v)
	}
}

// --- helmgen.HelmValuesGenerator ---

type MockHelmValuesGenerator struct {
	GenerateFn func(cfg *domain.ServiceConfig, env *domain.Environment, contexts map[string]domain.ResolvedContext) (*helmgen.HelmValues, error)
}

func (m *MockHelmValuesGenerator) Generate(cfg *domain.ServiceConfig, env *domain.Environment, contexts map[string]domain.ResolvedContext) (*helmgen.HelmValues, error) {
	if m.GenerateFn != nil {
		return m.GenerateFn(cfg, env, contexts)
	}
	return &helmgen.HelmValues{}, nil
}

// --- dependency.DependencyResolver ---

type MockDependencyResolver struct {
	ResolveAllFn func(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) (map[string]domain.ResolvedContext, error)
	ValidateFn   func(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) error
}

func (m *MockDependencyResolver) ResolveAll(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) (map[string]domain.ResolvedContext, error) {
	if m.ResolveAllFn != nil {
		return m.ResolveAllFn(ctx, serviceID, envID, deps)
	}
	return nil, nil
}
func (m *MockDependencyResolver) Validate(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) error {
	if m.ValidateFn != nil {
		return m.ValidateFn(ctx, serviceID, envID, deps)
	}
	return nil
}

// Compile-time interface checks.
var (
	_ store.DeploymentRepository                  = (*MockDeploymentRepository)(nil)
	_ store.DependencyDeploymentRequestRepository = (*MockDepRequestRepository)(nil)
	_ store.CommandRepository                     = (*MockCommandRepository)(nil)
	_ store.CommandLogRepository                  = (*MockCommandLogRepository)(nil)
	_ store.ClusterRepository                     = (*MockClusterRepository)(nil)
	_ events.Bus                                  = (*MockBus)(nil)
	_ helmgen.HelmValuesGenerator                 = (*MockHelmValuesGenerator)(nil)
	_ dependency.DependencyResolver               = (*MockDependencyResolver)(nil)
)
