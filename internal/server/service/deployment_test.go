package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
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
	deployRepo := &testutil.MockDeploymentRepository{}
	depConfigRepo := &testutil.MockDependencyConfigRepository{}
	depRequestRepo := &testutil.MockDepRequestRepository{}
	cmdRepo := &testutil.MockCommandRepository{}
	resolver := &testutil.MockDependencyResolver{}
	helmGen := &testutil.MockHelmValuesGenerator{}
	bus := &testutil.MockBus{}
	tx := &MockTransactor{
		RunInTxFn: func(ctx context.Context, fn func(ctx context.Context, tx TxRepos) error) error {
			repos := TxRepos{
				Deployments: deployRepo,
				DepRequests: depRequestRepo,
				Commands:    cmdRepo,
			}
			return fn(ctx, repos)
		},
	}

	s := NewDeploymentService(deployRepo, svcRepo, envRepo, depConfigRepo, depRequestRepo, resolver, helmGen, tx, bus, cmdRepo)

	t.Run("Submit without managed dependencies", func(t *testing.T) {
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
		deployRepo.CreateFn = func(ctx context.Context, d *domain.Deployment) error {
			createdDep = d
			return nil
		}

		var enqueuedCmd *domain.Command
		cmdRepo.EnqueueFn = func(ctx context.Context, cID uuid.UUID, cmd *domain.Command) error {
			if cID != clusterID {
				t.Errorf("expected clusterID %v, got %v", clusterID, cID)
			}
			enqueuedCmd = cmd
			return nil
		}

		deployRepo.SetHelmCommandIDFn = func(ctx context.Context, dID uuid.UUID, cmdID uuid.UUID) error {
			if dID != createdDep.ID {
				t.Errorf("expected deploymentID %v, got %v", createdDep.ID, dID)
			}
			if cmdID != enqueuedCmd.ID {
				t.Errorf("expected commandID %v, got %v", enqueuedCmd.ID, cmdID)
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
		if enqueuedCmd == nil {
			t.Fatal("expected enqueued command, got nil")
		}
		if enqueuedCmd.Type != "helm.upgrade" {
			t.Errorf("expected command type helm.upgrade, got %s", enqueuedCmd.Type)
		}
	})

	t.Run("Submit with managed dependencies", func(t *testing.T) {
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

		depConfigRepo.GetFn = func(ctx context.Context, sID, eID uuid.UUID, depName string) (*domain.DependencyConfig, error) {
			if depName == "db" {
				return &domain.DependencyConfig{Managed: true, ProviderInputs: map[string]any{"workDir": "/terraform/db"}}, nil
			}
			return &domain.DependencyConfig{Managed: false}, nil
		}

		var enqueuedCmds []*domain.Command
		cmdRepo.EnqueueFn = func(ctx context.Context, cID uuid.UUID, cmd *domain.Command) error {
			enqueuedCmds = append(enqueuedCmds, cmd)
			return nil
		}

		var createdDepReqs []*domain.DependencyDeploymentRequest
		depRequestRepo.CreateFn = func(ctx context.Context, req *domain.DependencyDeploymentRequest) error {
			createdDepReqs = append(createdDepReqs, req)
			return nil
		}

		d, err := s.Submit(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d == nil {
			t.Fatal("expected deployment, got nil")
		}

		// Should only have 1 command (tofu.apply for "db")
		// Redis is unmanaged, so it doesn't get a command yet.
		// Helm upgrade is NOT enqueued yet because it waits for managed deps.
		if len(enqueuedCmds) != 1 {
			t.Errorf("expected 1 enqueued command, got %d", len(enqueuedCmds))
		} else if enqueuedCmds[0].Type != "tofu.apply" {
			t.Errorf("expected command type tofu.apply, got %s", enqueuedCmds[0].Type)
		}

		if len(createdDepReqs) != 1 {
			t.Errorf("expected 1 dependency request, got %d", len(createdDepReqs))
		} else if createdDepReqs[0].DependencyName != "db" {
			t.Errorf("expected dependency name db, got %s", createdDepReqs[0].DependencyName)
		}
	})
}
