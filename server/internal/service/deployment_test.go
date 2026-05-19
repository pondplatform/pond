package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

type MockTransactor struct {
	RunInTxFn func(ctx context.Context, fn func(ctx context.Context, tx TxRepos) error) error
}

func (m *MockTransactor) RunInTx(ctx context.Context, fn func(ctx context.Context, tx TxRepos) error) error {
	if m.RunInTxFn != nil {
		return m.RunInTxFn(ctx, fn)
	}
	return nil
}

type mockDependencyService struct {
	createDependencyDeploymentsFn func(ctx context.Context, tx TxRepos, service *domain.Service, environment *domain.Environment, dep *domain.Deployment) ([]domain.DependencyDeployment, error)
	dependencyDeploymentStatusFn  func(ctx context.Context, tx TxRepos, deploymentId uuid.UUID) (domain.DependencyDeploymentStatus, error)
	scheduleCommandsFn            func(ctx context.Context, tx TxRepos, deploymentId uuid.UUID) error
	buildContextsFn               func(rawOutputs map[string]json.RawMessage) (map[string]map[string]any, error)
	validateFn                    func(ctx context.Context, deps map[string]serviceconfig.DependencyDeclaration) error
}

func (m *mockDependencyService) CreateDependencyDeployments(ctx context.Context, tx TxRepos, service *domain.Service, dep *domain.Deployment) (domain.DependencyDeploymentStatus, error) {
	if m.createDependencyDeploymentsFn != nil {
		return m.createDependencyDeploymentsFn(ctx, tx, service, environment, dep)
	}
	return nil, nil
}

func (m *mockDependencyService) DependencyDeploymentStatus(ctx context.Context, tx TxRepos, deploymentId uuid.UUID) (domain.DependencyDeploymentStatus, error) {
	if m.dependencyDeploymentStatusFn != nil {
		return m.dependencyDeploymentStatusFn(ctx, tx, deploymentId)
	}
	return domain.DependencyDeploymentStatusSucceeded, nil
}

func (m *mockDependencyService) ScheduleCommands(ctx context.Context, tx TxRepos, deploymentId uuid.UUID) error {
	if m.scheduleCommandsFn != nil {
		return m.scheduleCommandsFn(ctx, tx, deploymentId)
	}
	return nil
}

func (m *mockDependencyService) BuildContexts(rawOutputs map[string]json.RawMessage) (map[string]map[string]any, error) {
	if m.buildContextsFn != nil {
		return m.buildContextsFn(rawOutputs)
	}
	return map[string]map[string]any{}, nil
}

func (m *mockDependencyService) Validate(ctx context.Context, deps map[string]serviceconfig.DependencyDeclaration) error {
	if m.validateFn != nil {
		return m.validateFn(ctx, deps)
	}
	return nil
}

// mockConfigResolver is a simple resolver that returns the base config (ignores overrides).
type mockConfigResolver struct{}

func (m *mockConfigResolver) Resolve(base *serviceconfig.OverridableConfig, envName string) (*serviceconfig.ServiceConfig, error) {
	cfg := base.ServiceConfig
	if cfg.Env == nil {
		cfg.Env = make(map[string]string)
	}
	if cfg.Dependencies == nil {
		cfg.Dependencies = make(map[string]serviceconfig.DependencyDeclaration)
	}
	if cfg.Configs == nil {
		cfg.Configs = make(map[string]serviceconfig.ConfigFileSpec)
	}
	return &cfg, nil
}

func TestDeploymentService_Submit(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	envID := uuid.New()
	clusterID := uuid.New()
	svcID := uuid.New()

	svcRepo := &testutil.MockServiceRepository{
		GetByNameFn: func(ctx context.Context, pID uuid.UUID, name string) (*domain.Service, error) {
			return &domain.Service{ID: svcID, ProjectID: pID, Name: name}, nil
		},
	}
	envRepo := &testutil.MockEnvironmentRepository{
		GetByNameFn: func(ctx context.Context, projectID uuid.UUID, name string) (*domain.Environment, error) {
			return &domain.Environment{ID: envID, Name: name, ClusterID: clusterID}, nil
		},
	}
	infoStore := &testutil.MockDeploymentInfoStore{}
	depSvc := &mockDependencyService{}
	helmGen := &testutil.MockHelmValuesGenerator{}
	bus := &testutil.MockBus{}
	tx := &MockTransactor{
		RunInTxFn: func(ctx context.Context, fn func(ctx context.Context, tx TxRepos) error) error {
			return fn(ctx, TxRepos{DeploymentInfo: infoStore})
		},
	}

	tmplRenderer := NewTemplateRenderer()
	resolver := &mockConfigResolver{}
	s := NewDeploymentService(infoStore, svcRepo, envRepo, depSvc, helmGen, tmplRenderer, resolver, tx, bus, slog.Default())

	t.Run("Submit without dependencies", func(t *testing.T) {
		// No deps → CreateDependencyDeployments returns empty → helm enqueued immediately.
		depSvc.createDependencyDeploymentsFn = func(_ context.Context, _ TxRepos, _ *domain.Service, _ *domain.Environment, _ *domain.Deployment) ([]domain.DependencyDeployment, error) {
			return nil, nil
		}

		var createdDep *domain.Deployment
		infoStore.CreateFn = func(ctx context.Context, d *domain.Deployment) error {
			createdDep = d
			return nil
		}

		var createdCmd *domain.Command
		infoStore.CreateCommandFn = func(ctx context.Context, cmd *domain.Command) error {
			if cmd.ClusterID != clusterID {
				t.Errorf("expected clusterID %v, got %v", clusterID, cmd.ClusterID)
			}
			createdCmd = cmd
			return nil
		}

		infoStore.SetHelmCommandIDFn = func(ctx context.Context, dID uuid.UUID, cmdID uuid.UUID) error {
			if dID != createdDep.ID {
				t.Errorf("expected deploymentID %v, got %v", createdDep.ID, dID)
			}
			if cmdID != createdCmd.ID {
				t.Errorf("expected commandID %v, got %v", createdCmd.ID, cmdID)
			}
			return nil
		}

		infoStore.GetDepOutputsByDeploymentFn = func(ctx context.Context, deploymentID uuid.UUID) (map[string]json.RawMessage, error) {
			return map[string]json.RawMessage{}, nil
		}

		req := SubmitRequest{
			ProjectID:       projectID,
			EnvironmentName: "staging",
			OverridableConfig: serviceconfig.OverridableConfig{
				ServiceConfig: serviceconfig.ServiceConfig{
					Name: "my-service",
				},
			},
			ImageTag:    "v1",
			TriggeredBy: "user",
		}

		d, err := s.Submit(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d == nil {
			t.Fatal("expected deployment, got nil")
		}
		if d.ServiceID != svcID {
			t.Errorf("expected serviceID %v, got %v", svcID, d.ServiceID)
		}
		if d.EnvironmentID != envID {
			t.Errorf("expected environmentID %v, got %v", envID, d.EnvironmentID)
		}
		if d.Status != api.DeploymentStatusPending {
			t.Errorf("expected status %v, got %v", api.DeploymentStatusPending, d.Status)
		}
		if createdCmd == nil {
			t.Fatal("expected created command, got nil")
		}
		if createdCmd.Type != domain.CommandTypeHelmUpgrade {
			t.Errorf("expected command type helm.upgrade, got %s", createdCmd.Type)
		}
		if createdCmd.Status != domain.CommandStatusQueued {
			t.Errorf("expected command status queued, got %s", createdCmd.Status)
		}
	})

	t.Run("Submit with first-time dependencies (awaiting input)", func(t *testing.T) {
		// New deps with no prior config → awaiting_input → no commands created.
		depSvc.createDependencyDeploymentsFn = func(_ context.Context, _ TxRepos, _ *domain.Service, _ *domain.Environment, dep *domain.Deployment) ([]domain.DependencyDeployment, error) {
			return []domain.DependencyDeployment{
				{
					ID:             uuid.New(),
					DeploymentId:   dep.ID,
					DependencyName: "db",
					DependencyType: "postgres",
					Status:         domain.DependencyDeploymentStatusAwaitingInput,
				},
			}, nil
		}

		var updatedStatus domain.DeploymentStatus
		infoStore.UpdateStatusFn = func(ctx context.Context, id uuid.UUID, status domain.DeploymentStatus, completedAt *time.Time) error {
			updatedStatus = status
			return nil
		}

		var createdCmdCount int
		infoStore.CreateCommandFn = func(ctx context.Context, cmd *domain.Command) error {
			createdCmdCount++
			return nil
		}

		req := SubmitRequest{
			ProjectID:       projectID,
			EnvironmentName: "staging",
			OverridableConfig: serviceconfig.OverridableConfig{
				ServiceConfig: serviceconfig.ServiceConfig{
					Name: "my-service",
					Dependencies: map[string]serviceconfig.DependencyDeclaration{
						"db": {Type: "postgres"},
					},
				},
			},
			ImageTag:    "v1",
			TriggeredBy: "user",
		}

		d, err := s.Submit(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status != api.DeploymentStatusAwaitingInput {
			t.Errorf("expected status awaiting_input, got %v", d.Status)
		}
		if updatedStatus != api.DeploymentStatusAwaitingInput {
			t.Errorf("expected DB status awaiting_input, got %v", updatedStatus)
		}
		if createdCmdCount != 0 {
			t.Errorf("expected no commands created, got %d", createdCmdCount)
		}
	})

	t.Run("Submit with prior managed dependencies (schedule immediately)", func(t *testing.T) {
		// Re-deployment: deps have prior config → status=pending → ScheduleCommands called.
		managed := true
		cmdID := uuid.New()
		depID := uuid.New()
		depSvc.createDependencyDeploymentsFn = func(_ context.Context, _ TxRepos, _ *domain.Service, _ *domain.Environment, dep *domain.Deployment) ([]domain.DependencyDeployment, error) {
			return []domain.DependencyDeployment{
				{
					ID:             depID,
					DeploymentId:   dep.ID,
					DependencyName: "db",
					DependencyType: "postgres",
					Managed:        &managed,
					Status:         domain.DependencyDeploymentStatusPending,
				},
			}, nil
		}

		var scheduleCommandsCalled bool
		depSvc.scheduleCommandsFn = func(_ context.Context, _ TxRepos, deploymentId uuid.UUID) error {
			scheduleCommandsCalled = true
			return nil
		}

		infoStore.ListDepConfigsFn = func(ctx context.Context, deploymentID uuid.UUID) ([]domain.DependencyDeployment, error) {
			return []domain.DependencyDeployment{
				{
					ID:             depID,
					DependencyName: "db",
					DependencyType: "postgres",
					Managed:        &managed,
					Status:         domain.DependencyDeploymentStatusPending,
					CommandID:      &cmdID,
				},
			}, nil
		}

		req := SubmitRequest{
			ProjectID:       projectID,
			EnvironmentName: "staging",
			OverridableConfig: serviceconfig.OverridableConfig{
				ServiceConfig: serviceconfig.ServiceConfig{
					Name: "my-service",
					Dependencies: map[string]serviceconfig.DependencyDeclaration{
						"db": {Type: "postgres"},
					},
				},
			},
			ImageTag:    "v1",
			TriggeredBy: "user",
		}

		d, err := s.Submit(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d == nil {
			t.Fatal("expected deployment, got nil")
		}
		if !scheduleCommandsCalled {
			t.Error("expected ScheduleCommands to be called")
		}
	})
}
