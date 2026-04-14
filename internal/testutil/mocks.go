// Package testutil provides hand-rolled mock implementations of domain
// interfaces for use in unit tests across the project.
package testutil

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/config"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/events"
	"github.com/pondplatform/pond/internal/server/helmgen"
	"github.com/pondplatform/pond/internal/server/store"
)

// --- store.DeploymentInfoStore ---

type MockDeploymentInfoStore struct {
	// Deployment operations
	GetByIDFn            func(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	ListByServiceFn      func(ctx context.Context, serviceID uuid.UUID) ([]domain.Deployment, error)
	CreateFn             func(ctx context.Context, d *domain.Deployment) error
	UpdateStatusFn       func(ctx context.Context, id uuid.UUID, status domain.DeploymentStatus, completedAt *time.Time) error
	SetHelmCommandIDFn   func(ctx context.Context, id uuid.UUID, cmdID uuid.UUID) error
	GetByHelmCommandIDFn func(ctx context.Context, cmdID uuid.UUID) (*domain.Deployment, error)
	// Command operations (pure CRUD)
	CreateCommandFn               func(ctx context.Context, cmd *domain.Command) error
	GetCommandFn                  func(ctx context.Context, id uuid.UUID) (*domain.Command, error)
	UpdateCommandFn               func(ctx context.Context, cmd *domain.Command) error
	ListQueuedCommandsByClusterFn func(ctx context.Context, clusterID uuid.UUID) ([]*domain.Command, error)
	UpdateCommandsByDeploymentFn  func(ctx context.Context, deploymentID uuid.UUID, fromStatus, toStatus domain.CommandStatus) error
	// Command log operations
	AppendLogFn func(ctx context.Context, commandID uuid.UUID, line string) error
	// Dependency config operations
	CreateDepConfigFn          func(ctx context.Context, deploymentID uuid.UUID, cfg *domain.DeploymentDependencyConfig) error
	GetDepConfigByCommandIDFn  func(ctx context.Context, commandID uuid.UUID) (uuid.UUID, *domain.DeploymentDependencyConfig, error)
	MarkDepConfigSucceededFn   func(ctx context.Context, deploymentID uuid.UUID, depName string, output json.RawMessage) error
	MarkDepConfigFailedFn      func(ctx context.Context, deploymentID uuid.UUID, depName string) error
	AllDepConfigsCompleteFn    func(ctx context.Context, deploymentID uuid.UUID) (bool, bool, error)
	GetDepOutputsByDeploymentFn func(ctx context.Context, deploymentID uuid.UUID) (map[string]json.RawMessage, error)
}

func (m *MockDeploymentInfoStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}
func (m *MockDeploymentInfoStore) ListByService(ctx context.Context, serviceID uuid.UUID) ([]domain.Deployment, error) {
	if m.ListByServiceFn != nil {
		return m.ListByServiceFn(ctx, serviceID)
	}
	return nil, nil
}
func (m *MockDeploymentInfoStore) Create(ctx context.Context, d *domain.Deployment) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, d)
	}
	return nil
}
func (m *MockDeploymentInfoStore) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.DeploymentStatus, completedAt *time.Time) error {
	if m.UpdateStatusFn != nil {
		return m.UpdateStatusFn(ctx, id, status, completedAt)
	}
	return nil
}
func (m *MockDeploymentInfoStore) SetHelmCommandID(ctx context.Context, id uuid.UUID, cmdID uuid.UUID) error {
	if m.SetHelmCommandIDFn != nil {
		return m.SetHelmCommandIDFn(ctx, id, cmdID)
	}
	return nil
}
func (m *MockDeploymentInfoStore) GetByHelmCommandID(ctx context.Context, cmdID uuid.UUID) (*domain.Deployment, error) {
	if m.GetByHelmCommandIDFn != nil {
		return m.GetByHelmCommandIDFn(ctx, cmdID)
	}
	return nil, nil
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
func (m *MockDeploymentInfoStore) ListQueuedCommandsByCluster(ctx context.Context, clusterID uuid.UUID) ([]*domain.Command, error) {
	if m.ListQueuedCommandsByClusterFn != nil {
		return m.ListQueuedCommandsByClusterFn(ctx, clusterID)
	}
	return nil, nil
}
func (m *MockDeploymentInfoStore) UpdateCommandsByDeployment(ctx context.Context, deploymentID uuid.UUID, fromStatus, toStatus domain.CommandStatus) error {
	if m.UpdateCommandsByDeploymentFn != nil {
		return m.UpdateCommandsByDeploymentFn(ctx, deploymentID, fromStatus, toStatus)
	}
	return nil
}
func (m *MockDeploymentInfoStore) AppendLog(ctx context.Context, commandID uuid.UUID, line string) error {
	if m.AppendLogFn != nil {
		return m.AppendLogFn(ctx, commandID, line)
	}
	return nil
}
func (m *MockDeploymentInfoStore) CreateDepConfig(ctx context.Context, deploymentID uuid.UUID, cfg *domain.DeploymentDependencyConfig) error {
	if m.CreateDepConfigFn != nil {
		return m.CreateDepConfigFn(ctx, deploymentID, cfg)
	}
	return nil
}
func (m *MockDeploymentInfoStore) GetDepConfigByCommandID(ctx context.Context, commandID uuid.UUID) (uuid.UUID, *domain.DeploymentDependencyConfig, error) {
	if m.GetDepConfigByCommandIDFn != nil {
		return m.GetDepConfigByCommandIDFn(ctx, commandID)
	}
	return uuid.Nil, nil, nil
}
func (m *MockDeploymentInfoStore) MarkDepConfigSucceeded(ctx context.Context, deploymentID uuid.UUID, depName string, output json.RawMessage) error {
	if m.MarkDepConfigSucceededFn != nil {
		return m.MarkDepConfigSucceededFn(ctx, deploymentID, depName, output)
	}
	return nil
}
func (m *MockDeploymentInfoStore) MarkDepConfigFailed(ctx context.Context, deploymentID uuid.UUID, depName string) error {
	if m.MarkDepConfigFailedFn != nil {
		return m.MarkDepConfigFailedFn(ctx, deploymentID, depName)
	}
	return nil
}
func (m *MockDeploymentInfoStore) AllDepConfigsComplete(ctx context.Context, deploymentID uuid.UUID) (bool, bool, error) {
	if m.AllDepConfigsCompleteFn != nil {
		return m.AllDepConfigsCompleteFn(ctx, deploymentID)
	}
	return false, false, nil
}
func (m *MockDeploymentInfoStore) GetDepOutputsByDeployment(ctx context.Context, deploymentID uuid.UUID) (map[string]json.RawMessage, error) {
	if m.GetDepOutputsByDeploymentFn != nil {
		return m.GetDepOutputsByDeploymentFn(ctx, deploymentID)
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

// --- store.ClusterRepository ---

type MockClusterRepository struct {
	GetByIDFn            func(ctx context.Context, id uuid.UUID) (*domain.Cluster, error)
	GetByTokenHashFn     func(ctx context.Context, hash string) (*domain.Cluster, error)
	ListByOrganizationFn func(ctx context.Context, orgID uuid.UUID) ([]domain.Cluster, error)
	CreateFn             func(ctx context.Context, cluster *domain.Cluster) error
	UpdateLastSeenFn     func(ctx context.Context, id uuid.UUID, lastSeen time.Time) error
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
	GenerateFn func(cfg *domain.ServiceConfig, env *domain.Environment, contexts map[string]map[string]any) (*helmgen.HelmValues, error)
}

func (m *MockHelmValuesGenerator) Generate(cfg *domain.ServiceConfig, env *domain.Environment, contexts map[string]map[string]any) (*helmgen.HelmValues, error) {
	if m.GenerateFn != nil {
		return m.GenerateFn(cfg, env, contexts)
	}
	return &helmgen.HelmValues{}, nil
}

// --- config.TemplateRenderer ---

type MockTemplateRenderer struct {
	RenderFn func(values map[string]any, contexts map[string]map[string]any, svcConfig *domain.ServiceConfig) (map[string]any, error)
}

func (m *MockTemplateRenderer) Render(values map[string]any, contexts map[string]map[string]any, svcConfig *domain.ServiceConfig) (map[string]any, error) {
	if m.RenderFn != nil {
		return m.RenderFn(values, contexts, svcConfig)
	}
	return values, nil
}

// Compile-time interface checks.
var (
	_ store.DeploymentInfoStore   = (*MockDeploymentInfoStore)(nil)
	_ store.ClusterRepository     = (*MockClusterRepository)(nil)
	_ events.Bus                  = (*MockBus)(nil)
	_ helmgen.HelmValuesGenerator = (*MockHelmValuesGenerator)(nil)
	_ config.TemplateRenderer     = (*MockTemplateRenderer)(nil)
)
