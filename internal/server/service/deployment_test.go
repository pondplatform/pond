package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/config"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/testutil"
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
	scheduleCommandsFn func(ctx context.Context, tx TxRepos, service *domain.Service, environment *domain.Environment, dep *domain.Deployment) ([]domain.DependencyDeployment, error)
	buildContextsFn    func(rawOutputs map[string]json.RawMessage) (map[string]map[string]any, error)
	validateFn         func(ctx context.Context, deps map[string]domain.DependencyDeclaration) error
}

func (m *mockDependencyService) ScheduleCommands(ctx context.Context, tx TxRepos, service *domain.Service, environment *domain.Environment, dep *domain.Deployment) ([]domain.DependencyDeployment, error) {
	if m.scheduleCommandsFn != nil {
		return m.scheduleCommandsFn(ctx, tx, service, environment, dep)
	}
	return nil, nil
}

func (m *mockDependencyService) BuildContexts(rawOutputs map[string]json.RawMessage) (map[string]map[string]any, error) {
	if m.buildContextsFn != nil {
		return m.buildContextsFn(rawOutputs)
	}
	return map[string]map[string]any{}, nil
}

func (m *mockDependencyService) Validate(ctx context.Context, deps map[string]domain.DependencyDeclaration) error {
	if m.validateFn != nil {
		return m.validateFn(ctx, deps)
	}
	return nil
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
		GetByIDFn: func(ctx context.Context, id uuid.UUID) (*domain.Environment, error) {
			return &domain.Environment{ID: id, ClusterID: clusterID}, nil
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

	tmplRenderer := config.NewTemplateRenderer()
	s := NewDeploymentService(infoStore, svcRepo, envRepo, depSvc, helmGen, tmplRenderer, tx, bus)

	t.Run("Submit without managed dependencies", func(t *testing.T) {
		depSvc.scheduleCommandsFn = nil // returns nil (no managed deps)

		req := SubmitRequest{
			ProjectID:     projectID,
			EnvironmentID: envID,
			ServiceConfig: domain.ServiceConfig{
				Name: "my-service",
			},
			ImageTag:    "v1",
			TriggeredBy: "user",
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
		if d.Status != domain.DeploymentStatusPending {
			t.Errorf("expected status %v, got %v", domain.DeploymentStatusPending, d.Status)
		}
		if createdCmd == nil {
			t.Fatal("expected created command, got nil")
		}
		if createdCmd.Type != "helm.upgrade" {
			t.Errorf("expected command type helm.upgrade, got %s", createdCmd.Type)
		}
		if createdCmd.Status != domain.CommandStatusQueued {
			t.Errorf("expected command status queued, got %s", createdCmd.Status)
		}
	})

	t.Run("Submit with managed dependencies", func(t *testing.T) {
		var createdCmds []*domain.Command
		infoStore.CreateCommandFn = func(ctx context.Context, cmd *domain.Command) error {
			createdCmds = append(createdCmds, cmd)
			return nil
		}

		var createdDepCfgs []*domain.DependencyDeployment
		infoStore.CreateDepConfigFn = func(ctx context.Context, cfg *domain.DependencyDeployment) error {
			createdDepCfgs = append(createdDepCfgs, cfg)
			return nil
		}

		// ScheduleCommands creates the tofu command and dep config inside the tx.
		depSvc.scheduleCommandsFn = func(ctx context.Context, tx TxRepos, service *domain.Service, environment *domain.Environment, dep *domain.Deployment) ([]domain.DependencyDeployment, error) {
			now := time.Now()
			managed := true
			cmd := &domain.Command{
				ID:           uuid.New(),
				ClusterID:    environment.ClusterID,
				DeploymentID: dep.ID,
				Type:         "tofu.apply",
				Status:       domain.CommandStatusQueued,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			depCfg := domain.DependencyDeployment{
				ID:             uuid.New(),
				DependencyName: "db",
				DependencyType: "postgres",
				Managed:        &managed,
				Status:         domain.DependencyDeploymentStatusPending,
				CommandID:      &cmd.ID,
			}
			if err := tx.DeploymentInfo.CreateCommand(ctx, cmd); err != nil {
				return nil, err
			}
			if err := tx.DeploymentInfo.CreateDepConfig(ctx, &depCfg); err != nil {
				return nil, err
			}
			return []domain.DependencyDeployment{depCfg}, nil
		}

		req := SubmitRequest{
			ProjectID:     projectID,
			EnvironmentID: envID,
			ServiceConfig: domain.ServiceConfig{
				Name: "my-service",
				Dependencies: map[string]domain.DependencyDeclaration{
					"db":    {Type: "postgres", Config: map[string]any{"version": "13"}},
					"redis": {Type: "redis"},
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

		// Should only have 1 command (tofu.apply for "db").
		// Helm upgrade is NOT created yet — it waits for managed deps.
		if len(createdCmds) != 1 {
			t.Errorf("expected 1 created command, got %d", len(createdCmds))
		} else if createdCmds[0].Type != "tofu.apply" {
			t.Errorf("expected command type tofu.apply, got %s", createdCmds[0].Type)
		}

		if len(createdDepCfgs) != 1 {
			t.Errorf("expected 1 dep config, got %d", len(createdDepCfgs))
		} else if createdDepCfgs[0].DependencyName != "db" {
			t.Errorf("expected dependency name db, got %s", createdDepCfgs[0].DependencyName)
		}
	})
}
