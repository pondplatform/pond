package service

import (
	"context"
	"encoding/json"
	"errors"
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
	createDependencyDeploymentsFn func(ctx context.Context, tx TxRepos, service *domain.Service, dep *domain.Deployment, inputs map[string]api.DependencyInput) (domain.DependencyDeploymentStatus, error)
	dependencyDeploymentStatusFn  func(ctx context.Context, deploymentId uuid.UUID) (domain.DependencyDeploymentStatus, error)
	scheduleCommandsFn            func(ctx context.Context, deploymentId uuid.UUID) error
	handleCommandResultFn         func(ctx context.Context, commandID uuid.UUID) error
	buildContextsFn               func(rawOutputs map[string]json.RawMessage) (map[string]map[string]any, error)
	validateFn                    func(ctx context.Context, deps map[string]serviceconfig.DependencyDeclaration) error
}

func (m *mockDependencyService) CreateDependencyDeployments(ctx context.Context, tx TxRepos, service *domain.Service, dep *domain.Deployment, inputs map[string]api.DependencyInput) (domain.DependencyDeploymentStatus, error) {
	if m.createDependencyDeploymentsFn != nil {
		return m.createDependencyDeploymentsFn(ctx, tx, service, dep, inputs)
	}
	return domain.DependencyDeploymentStatusSucceeded, nil
}

func (m *mockDependencyService) DependencyDeploymentStatus(ctx context.Context, deploymentId uuid.UUID) (domain.DependencyDeploymentStatus, error) {
	if m.dependencyDeploymentStatusFn != nil {
		return m.dependencyDeploymentStatusFn(ctx, deploymentId)
	}
	return domain.DependencyDeploymentStatusSucceeded, nil
}

func (m *mockDependencyService) ScheduleCommands(ctx context.Context, deploymentId uuid.UUID) error {
	if m.scheduleCommandsFn != nil {
		return m.scheduleCommandsFn(ctx, deploymentId)
	}
	return nil
}

func (m *mockDependencyService) HandleCommandResult(ctx context.Context, commandID uuid.UUID) error {
	if m.handleCommandResultFn != nil {
		return m.handleCommandResultFn(ctx, commandID)
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
	topics := &testutil.MockTopics{}
	tx := &MockTransactor{
		RunInTxFn: func(ctx context.Context, fn func(ctx context.Context, tx TxRepos) error) error {
			return fn(ctx, TxRepos{DeploymentInfo: infoStore})
		},
	}

	tmplRenderer := NewTemplateRenderer()
	resolver := &mockConfigResolver{}
	s := NewDeploymentService(infoStore, svcRepo, envRepo, depSvc, helmGen, tmplRenderer, resolver, tx, topics, slog.Default())

	t.Run("Submit without dependencies", func(t *testing.T) {
		// No deps → CreateDependencyDeployments returns Succeeded → helm enqueued immediately.
		depSvc.createDependencyDeploymentsFn = func(_ context.Context, _ TxRepos, _ *domain.Service, _ *domain.Deployment, _ map[string]api.DependencyInput) (domain.DependencyDeploymentStatus, error) {
			return domain.DependencyDeploymentStatusSucceeded, nil
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
					Version: 1,
					Name:    "my-service",
					Image:   "ghcr.io/example/my-service",
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
		depSvc.createDependencyDeploymentsFn = func(_ context.Context, _ TxRepos, _ *domain.Service, _ *domain.Deployment, _ map[string]api.DependencyInput) (domain.DependencyDeploymentStatus, error) {
			return domain.DependencyDeploymentStatusAwaitingInput, nil
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
					Version: 1,
					Name:    "my-service",
					Image:   "ghcr.io/example/my-service",
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

	t.Run("Submit rejects an invalid probe before writing anything", func(t *testing.T) {
		var createCalled bool
		infoStore.CreateFn = func(ctx context.Context, d *domain.Deployment) error {
			createCalled = true
			return nil
		}

		req := SubmitRequest{
			ProjectID:       projectID,
			EnvironmentName: "staging",
			OverridableConfig: serviceconfig.OverridableConfig{
				ServiceConfig: serviceconfig.ServiceConfig{
					Version: 1,
					Name:    "my-service",
					Image:   "ghcr.io/example/my-service",
					Deployment: &serviceconfig.DeploymentSpec{
						Management: &serviceconfig.ManagementConfig{
							// No leading slash, and no service port to resolve against.
							ReadinessProbe: &serviceconfig.Probe{
								HTTPGet: &serviceconfig.HTTPGetAction{Path: "health"},
							},
						},
					},
				},
			},
			ImageTag:    "v1",
			TriggeredBy: "user",
		}

		_, err := s.Submit(ctx, req)
		if err == nil {
			t.Fatal("expected a validation error")
		}

		var ve *api.ValidationErrors
		if !errors.As(err, &ve) {
			t.Fatalf("expected *api.ValidationErrors so the handler returns 422, got %T: %v", err, err)
		}
		if len(ve.Errors) != 2 {
			t.Errorf("expected errors for both path and port, got %+v", ve.Errors)
		}
		if createCalled {
			t.Error("no deployment should be created when validation fails")
		}
	})

	t.Run("Submit with prior managed dependencies (schedule immediately)", func(t *testing.T) {
		// Re-deployment: deps have prior config → status=pending → ScheduleCommands called.
		depSvc.createDependencyDeploymentsFn = func(_ context.Context, _ TxRepos, _ *domain.Service, _ *domain.Deployment, _ map[string]api.DependencyInput) (domain.DependencyDeploymentStatus, error) {
			return domain.DependencyDeploymentStatusPending, nil
		}

		var scheduleCommandsCalled bool
		depSvc.scheduleCommandsFn = func(_ context.Context, deploymentId uuid.UUID) error {
			scheduleCommandsCalled = true
			return nil
		}

		req := SubmitRequest{
			ProjectID:       projectID,
			EnvironmentName: "staging",
			OverridableConfig: serviceconfig.OverridableConfig{
				ServiceConfig: serviceconfig.ServiceConfig{
					Version: 1,
					Name:    "my-service",
					Image:   "ghcr.io/example/my-service",
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

	withDBDep := func(deps map[string]api.DependencyInput) SubmitRequest {
		return SubmitRequest{
			ProjectID:       projectID,
			EnvironmentName: "staging",
			OverridableConfig: serviceconfig.OverridableConfig{
				ServiceConfig: serviceconfig.ServiceConfig{
					Version: 1,
					Name:    "my-service",
					Image:   "ghcr.io/example/my-service",
					Dependencies: map[string]serviceconfig.DependencyDeclaration{
						"db": {Type: "postgres"},
					},
				},
			},
			ImageTag:     "v1",
			TriggeredBy:  "user",
			Dependencies: deps,
		}
	}

	t.Run("Submit with inputs forwards them inside the transaction and schedules", func(t *testing.T) {
		inputs := map[string]api.DependencyInput{"db": {Managed: true, Values: map[string]any{"instances": 1}}}

		var inTx bool
		tx.RunInTxFn = func(ctx context.Context, fn func(ctx context.Context, tx TxRepos) error) error {
			inTx = true
			defer func() { inTx = false }()
			return fn(ctx, TxRepos{DeploymentInfo: infoStore})
		}

		var forwarded map[string]api.DependencyInput
		depSvc.createDependencyDeploymentsFn = func(_ context.Context, _ TxRepos, _ *domain.Service, _ *domain.Deployment, got map[string]api.DependencyInput) (domain.DependencyDeploymentStatus, error) {
			if !inTx {
				t.Error("dependency deployments must be created inside the transaction")
			}
			forwarded = got
			return domain.DependencyDeploymentStatusPending, nil
		}

		var scheduleCommandsCalled bool
		depSvc.scheduleCommandsFn = func(_ context.Context, _ uuid.UUID) error {
			scheduleCommandsCalled = true
			return nil
		}

		d, err := s.Submit(ctx, withDBDep(inputs))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status != api.DeploymentStatusPending {
			t.Errorf("expected status pending, got %v", d.Status)
		}
		if forwarded["db"].Managed != true || forwarded["db"].Values["instances"] != 1 {
			t.Errorf("expected inputs forwarded unchanged, got %+v", forwarded)
		}
		if !scheduleCommandsCalled {
			t.Error("expected ScheduleCommands to be called")
		}
	})

	t.Run("Submit rejects incomplete or unknown inputs before writing anything", func(t *testing.T) {
		svcRepo.GetByNameFn = func(context.Context, uuid.UUID, string) (*domain.Service, error) {
			return nil, api.ErrNotFound
		}
		var serviceCreated, txRan bool
		svcRepo.CreateFn = func(context.Context, *domain.Service) error {
			serviceCreated = true
			return nil
		}
		tx.RunInTxFn = func(context.Context, func(ctx context.Context, tx TxRepos) error) error {
			txRan = true
			return nil
		}

		cases := map[string]struct {
			inputs map[string]api.DependencyInput
			want   []api.ValidationError
		}{
			"missing": {
				inputs: map[string]api.DependencyInput{},
				want:   []api.ValidationError{{Component: "dependency", Field: "db", Message: "missing input"}},
			},
			"unknown": {
				inputs: map[string]api.DependencyInput{"db": {Managed: true}, "cache": {Managed: true}},
				want:   []api.ValidationError{{Component: "dependency", Field: "cache", Message: "not declared in service config"}},
			},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				req := withDBDep(tc.inputs)
				req.CreateIfNotExists = true
				_, err := s.Submit(ctx, req)

				var ve *api.ValidationErrors
				if !errors.As(err, &ve) {
					t.Fatalf("expected *api.ValidationErrors so the handler returns 422, got %T: %v", err, err)
				}
				if len(ve.Errors) != len(tc.want) {
					t.Fatalf("expected %+v, got %+v", tc.want, ve.Errors)
				}
				for i := range tc.want {
					if ve.Errors[i] != tc.want[i] {
						t.Errorf("error %d: expected %+v, got %+v", i, tc.want[i], ve.Errors[i])
					}
				}
				if serviceCreated || txRan {
					t.Error("nothing should be written when inputs are rejected")
				}
			})
		}
	})
}

func TestConfigureDeployment(t *testing.T) {
	deploymentID := uuid.New()
	store := &testutil.MockDeploymentInfoStore{
		GetDepConfigFn: func(_ context.Context, _ uuid.UUID, name string) (*domain.DependencyDeployment, error) {
			return &domain.DependencyDeployment{}, nil
		},
		UpdateDepConfigFn: func(_ context.Context, _ *domain.DependencyDeployment) error { return nil },
	}
	inputs := map[string]api.DependencyInput{"db": {Managed: true}}

	t.Run("publishes UserInputProvided", func(t *testing.T) {
		topics := &testutil.MockTopics{}
		svc := makeAdvanceSvc(store, nil, nil, nil, topics)
		if err := svc.ConfigureDeployment(context.Background(), deploymentID, inputs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		published := topics.UserInputProvided.Published()
		if len(published) != 1 || published[0].DeploymentID != deploymentID {
			t.Errorf("expected one UserInputProvided for %v, got %+v", deploymentID, published)
		}
	})

	t.Run("propagates publish error", func(t *testing.T) {
		publishErr := errors.New("broker down")
		topics := &testutil.MockTopics{}
		topics.UserInputProvided.PublishErr = publishErr
		svc := makeAdvanceSvc(store, nil, nil, nil, topics)
		if err := svc.ConfigureDeployment(context.Background(), deploymentID, inputs); !errors.Is(err, publishErr) {
			t.Fatalf("expected publish error, got %v", err)
		}
	})
}
