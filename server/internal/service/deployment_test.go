package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
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

// inlineTx runs every transaction directly against store.
func inlineTx(s store.DeploymentInfoStore) *MockTransactor {
	return &MockTransactor{
		RunInTxFn: func(ctx context.Context, fn func(ctx context.Context, tx TxRepos) error) error {
			return fn(ctx, TxRepos{DeploymentInfo: s})
		},
	}
}

type mockDependencyService struct {
	validateFn        func(ctx context.Context, deps map[string]serviceconfig.DependencyDeclaration) error
	scheduleCommandFn func(ctx context.Context, deployment db.Deployment, dependency db.DependencyDeployment) error
}

func (m *mockDependencyService) Validate(ctx context.Context, deps map[string]serviceconfig.DependencyDeclaration) error {
	if m.validateFn != nil {
		return m.validateFn(ctx, deps)
	}
	return nil
}

func (m *mockDependencyService) ScheduleCommand(ctx context.Context, deployment db.Deployment, dependency db.DependencyDeployment) error {
	if m.scheduleCommandFn != nil {
		return m.scheduleCommandFn(ctx, deployment, dependency)
	}
	return nil
}

type mockStateMachine struct {
	initializeDeploymentFn   func(ctx context.Context, deployment db.Deployment, dependencies []db.DependencyDeployment) error
	advanceOnCommandResultFn func(ctx context.Context, result events.CommandResult) error
}

func (m *mockStateMachine) InitializeDeployment(ctx context.Context, deployment db.Deployment, dependencies []db.DependencyDeployment) error {
	if m.initializeDeploymentFn != nil {
		return m.initializeDeploymentFn(ctx, deployment, dependencies)
	}
	return nil
}

func (m *mockStateMachine) AdvanceOnCommandResult(ctx context.Context, result events.CommandResult) error {
	if m.advanceOnCommandResultFn != nil {
		return m.advanceOnCommandResultFn(ctx, result)
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
	svcID := uuid.New()

	svcRepo := &testutil.MockServiceRepository{}
	envRepo := &testutil.MockEnvironmentRepository{
		GetByNameFn: func(ctx context.Context, projectID uuid.UUID, name string) (*db.Environment, error) {
			return &db.Environment{ID: envID, Name: name}, nil
		},
	}
	infoStore := &testutil.MockDeploymentInfoStore{}
	machine := &mockStateMachine{}
	tx := &MockTransactor{}
	s := &deploymentService{
		deploymentInfo: infoStore,
		services:       svcRepo,
		envs:           envRepo,
		dependencies:   &mockDependencyService{},
		stateMachine:   machine,
		resolver:       &mockConfigResolver{},
		tx:             tx,
	}

	// reset restores the defaults every subtest starts from.
	reset := func() {
		*svcRepo = testutil.MockServiceRepository{
			GetByNameFn: func(ctx context.Context, pID uuid.UUID, name string) (*db.Service, error) {
				return &db.Service{ID: svcID, ProjectID: pID, Name: name}, nil
			},
		}
		*infoStore = testutil.MockDeploymentInfoStore{}
		*machine = mockStateMachine{}
		*tx = *inlineTx(infoStore)
	}

	request := func(deps map[string]serviceconfig.DependencyDeclaration, inputs map[string]api.DependencyInput) SubmitRequest {
		return SubmitRequest{
			ProjectID:       projectID,
			EnvironmentName: "staging",
			OverridableConfig: serviceconfig.OverridableConfig{
				ServiceConfig: serviceconfig.ServiceConfig{
					Version:      1,
					Name:         "my-service",
					Image:        "ghcr.io/example/my-service",
					Dependencies: deps,
				},
			},
			ImageTag:     "v1",
			TriggeredBy:  "user",
			Dependencies: inputs,
		}
	}
	dbDep := map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}}

	t.Run("without dependencies creates a pending deployment and initializes it", func(t *testing.T) {
		reset()
		var created *db.Deployment
		infoStore.CreateFn = func(_ context.Context, d *db.Deployment) error {
			created = d
			return nil
		}
		var initialized *db.Deployment
		machine.initializeDeploymentFn = func(_ context.Context, d db.Deployment, deps []db.DependencyDeployment) error {
			initialized = &d
			if len(deps) != 0 {
				t.Errorf("expected no dependencies, got %+v", deps)
			}
			return nil
		}

		d, err := s.Submit(ctx, request(nil, nil))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.ServiceID != svcID || d.EnvironmentID != envID {
			t.Errorf("expected service %v and environment %v, got %v and %v", svcID, envID, d.ServiceID, d.EnvironmentID)
		}
		if d.Status != api.DeploymentStatusPending {
			t.Errorf("expected status pending, got %v", d.Status)
		}
		if created == nil || created.Status != api.DeploymentStatusPending {
			t.Fatalf("expected the deployment to be stored as pending, got %+v", created)
		}
		if initialized == nil || initialized.ID != d.ID {
			t.Errorf("expected the created deployment to be initialized, got %+v", initialized)
		}
	})

	t.Run("with first-time dependencies awaits input", func(t *testing.T) {
		reset()
		var createdDeps []db.DependencyDeployment
		infoStore.CreateDepConfigFn = func(_ context.Context, cfg *db.DependencyDeployment) error {
			createdDeps = append(createdDeps, *cfg)
			return nil
		}

		d, err := s.Submit(ctx, request(dbDep, nil))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status != api.DeploymentStatusAwaitingInput {
			t.Errorf("expected status awaiting_input, got %v", d.Status)
		}
		if len(createdDeps) != 1 || createdDeps[0].Status != db.DependencyDeploymentStatusAwaitingInput {
			t.Errorf("expected one awaiting_input dependency, got %+v", createdDeps)
		}
	})

	t.Run("with inputs creates configured dependencies inside the transaction", func(t *testing.T) {
		reset()
		var inTx bool
		tx.RunInTxFn = func(ctx context.Context, fn func(ctx context.Context, tx TxRepos) error) error {
			inTx = true
			defer func() { inTx = false }()
			return fn(ctx, TxRepos{DeploymentInfo: infoStore})
		}
		infoStore.CreateDepConfigFn = func(_ context.Context, _ *db.DependencyDeployment) error {
			if !inTx {
				t.Error("dependency deployments must be created inside the transaction")
			}
			return nil
		}
		var initialized []db.DependencyDeployment
		machine.initializeDeploymentFn = func(_ context.Context, _ db.Deployment, deps []db.DependencyDeployment) error {
			initialized = deps
			return nil
		}

		inputs := map[string]api.DependencyInput{"db": {Managed: true, Values: map[string]any{"instances": 1}}}
		d, err := s.Submit(ctx, request(dbDep, inputs))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status != api.DeploymentStatusPending {
			t.Errorf("expected status pending, got %v", d.Status)
		}
		if len(initialized) != 1 || !isManaged(initialized[0]) || initialized[0].ProviderInputs["instances"] != 1 {
			t.Errorf("expected a managed db dependency with its inputs, got %+v", initialized)
		}
	})

	t.Run("carries over the current deployment's dependency config", func(t *testing.T) {
		reset()
		currentID := uuid.New()
		managed := true
		svcRepo.GetByNameFn = func(_ context.Context, pID uuid.UUID, name string) (*db.Service, error) {
			return &db.Service{ID: svcID, ProjectID: pID, Name: name, CurrentDeploymentID: &currentID}, nil
		}
		infoStore.ListDepConfigsFn = func(_ context.Context, id uuid.UUID) ([]db.DependencyDeployment, error) {
			if id != currentID {
				t.Errorf("expected dependencies of %v, got %v", currentID, id)
			}
			return []db.DependencyDeployment{{DependencyName: "db", Managed: &managed, ProviderInputs: map[string]any{"instances": 2}}}, nil
		}
		var initialized []db.DependencyDeployment
		machine.initializeDeploymentFn = func(_ context.Context, _ db.Deployment, deps []db.DependencyDeployment) error {
			initialized = deps
			return nil
		}

		d, err := s.Submit(ctx, request(dbDep, nil))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.Status != api.DeploymentStatusPending {
			t.Errorf("expected status pending, got %v", d.Status)
		}
		if len(initialized) != 1 || initialized[0].Status != db.DependencyDeploymentStatusPending || initialized[0].ProviderInputs["instances"] != 2 {
			t.Errorf("expected the db config carried over as pending, got %+v", initialized)
		}
	})

	t.Run("propagates an initialization error", func(t *testing.T) {
		reset()
		initErr := errors.New("broker down")
		machine.initializeDeploymentFn = func(context.Context, db.Deployment, []db.DependencyDeployment) error {
			return initErr
		}
		if _, err := s.Submit(ctx, request(nil, nil)); !errors.Is(err, initErr) {
			t.Fatalf("expected initialization error, got %v", err)
		}
	})

	t.Run("rejects an invalid probe before writing anything", func(t *testing.T) {
		reset()
		var createCalled bool
		infoStore.CreateFn = func(ctx context.Context, d *db.Deployment) error {
			createCalled = true
			return nil
		}

		req := request(nil, nil)
		req.OverridableConfig.ServiceConfig.Deployment = &serviceconfig.DeploymentSpec{
			Management: &serviceconfig.ManagementConfig{
				// No leading slash, and no service port to resolve against.
				ReadinessProbe: &serviceconfig.Probe{
					HTTPGet: &serviceconfig.HTTPGetAction{Path: "health"},
				},
			},
		}

		_, err := s.Submit(ctx, req)
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

	t.Run("rejects incomplete or unknown inputs before writing anything", func(t *testing.T) {
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
				reset()
				svcRepo.GetByNameFn = func(context.Context, uuid.UUID, string) (*db.Service, error) {
					return nil, api.ErrNotFound
				}
				var serviceCreated, txRan bool
				svcRepo.CreateFn = func(context.Context, *db.Service) error {
					serviceCreated = true
					return nil
				}
				tx.RunInTxFn = func(context.Context, func(ctx context.Context, tx TxRepos) error) error {
					txRan = true
					return nil
				}

				req := request(dbDep, tc.inputs)
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
	ctx := context.Background()
	deploymentID := uuid.New()
	deployment := &db.Deployment{
		ID: deploymentID,
		ServiceConfigSnapshot: serviceconfig.ServiceConfig{
			Dependencies: map[string]serviceconfig.DependencyDeclaration{
				"db":    {Type: "postgres"},
				"cache": {Type: "http-service"},
			},
		},
	}
	current := []db.DependencyDeployment{
		{DependencyName: "cache", Status: db.DependencyDeploymentStatusAwaitingInput},
		{DependencyName: "db", Status: db.DependencyDeploymentStatusAwaitingInput},
	}

	makeService := func(store *testutil.MockDeploymentInfoStore, machine *mockStateMachine) *deploymentService {
		if store.GetByIDFn == nil {
			store.GetByIDFn = func(context.Context, uuid.UUID) (*db.Deployment, error) { return deployment, nil }
		}
		store.ListDepConfigsFn = func(context.Context, uuid.UUID) ([]db.DependencyDeployment, error) {
			return append([]db.DependencyDeployment(nil), current...), nil
		}
		return &deploymentService{deploymentInfo: store, stateMachine: machine}
	}

	t.Run("writes only the configured rows and initializes with all dependencies", func(t *testing.T) {
		var updated []db.DependencyDeployment
		store := &testutil.MockDeploymentInfoStore{
			UpdateDepConfigFn: func(_ context.Context, cfg *db.DependencyDeployment) error {
				updated = append(updated, *cfg)
				return nil
			},
		}
		var initialized []db.DependencyDeployment
		machine := &mockStateMachine{
			initializeDeploymentFn: func(_ context.Context, d db.Deployment, deps []db.DependencyDeployment) error {
				if d.ID != deploymentID {
					t.Errorf("expected deployment %v, got %v", deploymentID, d.ID)
				}
				initialized = deps
				return nil
			},
		}

		inputs := map[string]api.DependencyInput{"db": {Managed: true, Values: map[string]any{"instances": 1}}}
		if err := makeService(store, machine).ConfigureDeployment(ctx, deploymentID, inputs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(updated) != 1 || updated[0].DependencyName != "db" || !isManaged(updated[0]) || updated[0].Status != db.DependencyDeploymentStatusPending {
			t.Errorf("expected only db updated to managed pending, got %+v", updated)
		}
		if len(initialized) != 2 {
			t.Fatalf("expected both dependencies passed to the state machine, got %+v", initialized)
		}
		if initialized[0].Status != db.DependencyDeploymentStatusAwaitingInput || initialized[1].Status != db.DependencyDeploymentStatusPending {
			t.Errorf("expected cache still awaiting input and db pending, got %+v", initialized)
		}
	})

	t.Run("rejects undeclared inputs before writing anything", func(t *testing.T) {
		store := &testutil.MockDeploymentInfoStore{
			UpdateDepConfigFn: func(context.Context, *db.DependencyDeployment) error {
				t.Error("no dependency should be written when inputs are rejected")
				return nil
			},
		}
		machine := &mockStateMachine{
			initializeDeploymentFn: func(context.Context, db.Deployment, []db.DependencyDeployment) error {
				t.Error("the state machine must not run when inputs are rejected")
				return nil
			},
		}

		inputs := map[string]api.DependencyInput{"db": {Managed: true}, "nope": {Managed: true}}
		err := makeService(store, machine).ConfigureDeployment(ctx, deploymentID, inputs)
		var ve *api.ValidationErrors
		if !errors.As(err, &ve) {
			t.Fatalf("expected *api.ValidationErrors, got %T: %v", err, err)
		}
	})

	t.Run("unknown deployment is not found", func(t *testing.T) {
		store := &testutil.MockDeploymentInfoStore{
			GetByIDFn: func(context.Context, uuid.UUID) (*db.Deployment, error) {
				return nil, api.NotFound("deployment not found")
			},
		}
		err := makeService(store, &mockStateMachine{}).ConfigureDeployment(ctx, deploymentID, nil)
		if !errors.Is(err, api.ErrNotFound) {
			t.Fatalf("expected not found, got %v", err)
		}
	})
}
