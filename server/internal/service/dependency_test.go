package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/dependency"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// makeDependencyService builds a real dependencyService with the provided mocks.
func makeDependencyService(
	specs dependency.SpecRegistry,
	envs *testutil.MockEnvironmentRepository,
	store *testutil.MockDeploymentInfoStore,
) *internalDependencyService {
	if specs == nil {
		specs = dependency.NewSpecRegistry()
	}
	if envs == nil {
		envs = &testutil.MockEnvironmentRepository{}
	}
	if store == nil {
		store = &testutil.MockDeploymentInfoStore{}
	}
	return &internalDependencyService{specs: specs, envs: envs, deploymentInfo: store}
}

// --- computeStatus ---

func TestComputeStatus_FailedWinsOverAll(t *testing.T) {
	svc := &internalDependencyService{}
	deps := []domain.DependencyDeployment{
		{Status: domain.DependencyDeploymentStatusSucceeded},
		{Status: domain.DependencyDeploymentStatusFailed},
		{Status: domain.DependencyDeploymentStatusAwaitingInput},
	}
	if got := svc.computeStatus(deps); got != domain.DependencyDeploymentStatusFailed {
		t.Errorf("expected failed, got %s", got)
	}
}

func TestComputeStatus_AwaitingInputBeforesPending(t *testing.T) {
	svc := &internalDependencyService{}
	deps := []domain.DependencyDeployment{
		{Status: domain.DependencyDeploymentStatusPending},
		{Status: domain.DependencyDeploymentStatusAwaitingInput},
		{Status: domain.DependencyDeploymentStatusSucceeded},
	}
	if got := svc.computeStatus(deps); got != domain.DependencyDeploymentStatusAwaitingInput {
		t.Errorf("expected awaiting_input, got %s", got)
	}
}

func TestComputeStatus_PendingBeforeSucceeded(t *testing.T) {
	svc := &internalDependencyService{}
	deps := []domain.DependencyDeployment{
		{Status: domain.DependencyDeploymentStatusSucceeded},
		{Status: domain.DependencyDeploymentStatusPending},
	}
	if got := svc.computeStatus(deps); got != domain.DependencyDeploymentStatusPending {
		t.Errorf("expected pending, got %s", got)
	}
}

func TestComputeStatus_AllSucceededReturnsSucceeded(t *testing.T) {
	svc := &internalDependencyService{}
	deps := []domain.DependencyDeployment{
		{Status: domain.DependencyDeploymentStatusSucceeded},
		{Status: domain.DependencyDeploymentStatusSucceeded},
	}
	if got := svc.computeStatus(deps); got != domain.DependencyDeploymentStatusSucceeded {
		t.Errorf("expected succeeded, got %s", got)
	}
}

func TestComputeStatus_EmptySliceReturnsSucceeded(t *testing.T) {
	svc := &internalDependencyService{}
	if got := svc.computeStatus(nil); got != domain.DependencyDeploymentStatusSucceeded {
		t.Errorf("expected succeeded for empty deps, got %s", got)
	}
}

// --- CreateDependencyDeployments ---

func TestCreateDependencyDeployments_FirstTimeReturnsAwaitingInput(t *testing.T) {
	ctx := context.Background()
	svcID := uuid.New()
	depID := uuid.New()

	// Service has no prior deployment → previousConfig will be nil for each dep
	service := &domain.Service{ID: svcID}
	deployment := &domain.Deployment{
		ID: depID,
		ServiceConfigSnapshot: serviceconfig.ServiceConfig{
			Name: "my-svc",
			Dependencies: map[string]serviceconfig.DependencyDeclaration{
				"db": {Type: "postgres"},
			},
		},
	}

	var createdConfigs []*domain.DependencyDeployment
	txStore := &testutil.MockDeploymentInfoStore{
		CreateDepConfigFn: func(_ context.Context, cfg *domain.DependencyDeployment) error {
			createdConfigs = append(createdConfigs, cfg)
			return nil
		},
	}

	svc := makeDependencyService(nil, nil, nil)
	status, err := svc.CreateDependencyDeployments(ctx, TxRepos{DeploymentInfo: txStore}, service, deployment, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != domain.DependencyDeploymentStatusAwaitingInput {
		t.Errorf("expected awaiting_input for first-time dep, got %s", status)
	}
	if len(createdConfigs) != 1 {
		t.Fatalf("expected 1 dep config created, got %d", len(createdConfigs))
	}
	cfg := createdConfigs[0]
	if cfg.DependencyName != "db" {
		t.Errorf("expected dep name 'db', got %q", cfg.DependencyName)
	}
	if cfg.Status != domain.DependencyDeploymentStatusAwaitingInput {
		t.Errorf("expected config status awaiting_input, got %s", cfg.Status)
	}
	if cfg.Managed != nil {
		t.Error("expected Managed to be nil for first-time dep")
	}
}

func TestCreateDependencyDeployments_RedeployCarriesOverPriorConfig(t *testing.T) {
	ctx := context.Background()
	prevDepID := uuid.New()
	managed := true
	prevConfig := &domain.DependencyDeployment{
		DependencyName: "db",
		Managed:        &managed,
		ProviderInputs: map[string]any{"region": "eu-west-1"},
		UserConfig:     map[string]any{"version": "14"},
		Output:         json.RawMessage(`{"host":"db.example.com"}`),
		Status:         domain.DependencyDeploymentStatusSucceeded,
	}

	// Service has CurrentDeploymentID pointing to a prior deployment
	service := &domain.Service{ID: uuid.New(), CurrentDeploymentID: &prevDepID}
	deployment := &domain.Deployment{
		ID: uuid.New(),
		ServiceConfigSnapshot: serviceconfig.ServiceConfig{
			Name: "my-svc",
			Dependencies: map[string]serviceconfig.DependencyDeclaration{
				"db": {Type: "postgres"},
			},
		},
	}

	var createdCfg *domain.DependencyDeployment
	txStore := &testutil.MockDeploymentInfoStore{
		GetDepConfigFn: func(_ context.Context, _ uuid.UUID, _ string) (*domain.DependencyDeployment, error) {
			return prevConfig, nil
		},
		CreateDepConfigFn: func(_ context.Context, cfg *domain.DependencyDeployment) error {
			createdCfg = cfg
			return nil
		},
	}

	svc := makeDependencyService(nil, nil, nil)
	status, err := svc.CreateDependencyDeployments(ctx, TxRepos{DeploymentInfo: txStore}, service, deployment, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != domain.DependencyDeploymentStatusPending {
		t.Errorf("expected pending for redeploy with prior config, got %s", status)
	}
	if createdCfg == nil {
		t.Fatal("expected dep config created")
	}
	if createdCfg.Managed == nil || !*createdCfg.Managed {
		t.Error("expected Managed to be carried over as true")
	}
	if createdCfg.Status != domain.DependencyDeploymentStatusPending {
		t.Errorf("expected status pending for redeploy, got %s", createdCfg.Status)
	}
}

func TestCreateDependencyDeployments_NoDepsReturnsSucceeded(t *testing.T) {
	ctx := context.Background()
	service := &domain.Service{ID: uuid.New()}
	deployment := &domain.Deployment{
		ID: uuid.New(),
		ServiceConfigSnapshot: serviceconfig.ServiceConfig{
			Name:         "no-deps-svc",
			Dependencies: map[string]serviceconfig.DependencyDeclaration{},
		},
	}

	svc := makeDependencyService(nil, nil, nil)
	status, err := svc.CreateDependencyDeployments(ctx, TxRepos{DeploymentInfo: &testutil.MockDeploymentInfoStore{}}, service, deployment, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != domain.DependencyDeploymentStatusSucceeded {
		t.Errorf("expected succeeded for no-deps service, got %s", status)
	}
}

func TestCreateDependencyDeployments_WithInputReturnsPending(t *testing.T) {
	ctx := context.Background()
	service := &domain.Service{ID: uuid.New()}
	deployment := &domain.Deployment{
		ID: uuid.New(),
		ServiceConfigSnapshot: serviceconfig.ServiceConfig{
			Name: "my-svc",
			Dependencies: map[string]serviceconfig.DependencyDeclaration{
				"db":  {Type: "postgres"},
				"api": {Type: "http-service"},
			},
		},
	}
	inputs := map[string]api.DependencyInput{
		"db":  {Managed: true, Values: map[string]any{"instances": 1}},
		"api": {Managed: false, Values: map[string]any{"url": "https://api.example.com"}},
	}

	created := map[string]*domain.DependencyDeployment{}
	txStore := &testutil.MockDeploymentInfoStore{
		CreateDepConfigFn: func(_ context.Context, cfg *domain.DependencyDeployment) error {
			created[cfg.DependencyName] = cfg
			return nil
		},
	}

	svc := makeDependencyService(nil, nil, nil)
	status, err := svc.CreateDependencyDeployments(ctx, TxRepos{DeploymentInfo: txStore}, service, deployment, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != domain.DependencyDeploymentStatusPending {
		t.Errorf("expected pending when every dep has input, got %s", status)
	}

	db := created["db"]
	if db == nil || db.Managed == nil || !*db.Managed || db.Status != domain.DependencyDeploymentStatusPending {
		t.Fatalf("expected managed pending db config, got %+v", db)
	}
	if db.ProviderInputs["instances"] != 1 || len(db.UserConfig) != 0 {
		t.Errorf("expected managed values in ProviderInputs only, got provider=%v user=%v", db.ProviderInputs, db.UserConfig)
	}

	apiDep := created["api"]
	if apiDep == nil || apiDep.Managed == nil || *apiDep.Managed || apiDep.Status != domain.DependencyDeploymentStatusPending {
		t.Fatalf("expected unmanaged pending api config, got %+v", apiDep)
	}
	if apiDep.UserConfig["url"] != "https://api.example.com" || len(apiDep.ProviderInputs) != 0 {
		t.Errorf("expected unmanaged values in UserConfig only, got provider=%v user=%v", apiDep.ProviderInputs, apiDep.UserConfig)
	}
}

func TestCreateDependencyDeployments_InputOverridesPriorConfig(t *testing.T) {
	ctx := context.Background()
	prevDepID := uuid.New()
	service := &domain.Service{ID: uuid.New(), CurrentDeploymentID: &prevDepID}
	deployment := &domain.Deployment{
		ID: uuid.New(),
		ServiceConfigSnapshot: serviceconfig.ServiceConfig{
			Name: "my-svc",
			Dependencies: map[string]serviceconfig.DependencyDeclaration{
				"db": {Type: "postgres"},
			},
		},
	}

	var createdCfg *domain.DependencyDeployment
	txStore := &testutil.MockDeploymentInfoStore{
		GetDepConfigFn: func(_ context.Context, _ uuid.UUID, _ string) (*domain.DependencyDeployment, error) {
			t.Error("prior config must not be looked up when input is supplied")
			return nil, api.ErrNotFound
		},
		CreateDepConfigFn: func(_ context.Context, cfg *domain.DependencyDeployment) error {
			createdCfg = cfg
			return nil
		},
	}

	svc := makeDependencyService(nil, nil, nil)
	inputs := map[string]api.DependencyInput{"db": {Managed: false, Values: map[string]any{"host": "db.internal"}}}
	if _, err := svc.CreateDependencyDeployments(ctx, TxRepos{DeploymentInfo: txStore}, service, deployment, inputs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if createdCfg == nil || createdCfg.Managed == nil || *createdCfg.Managed {
		t.Fatalf("expected unmanaged config from input, got %+v", createdCfg)
	}
	if createdCfg.UserConfig["host"] != "db.internal" || createdCfg.Output != nil {
		t.Errorf("expected input values and no carried-over output, got user=%v output=%s", createdCfg.UserConfig, createdCfg.Output)
	}
}

// --- ScheduleCommands ---

func TestScheduleCommands_NonManagedDepMarkedSucceeded(t *testing.T) {
	ctx := context.Background()
	deploymentID := uuid.New()
	nonManaged := false

	var markedSucceeded bool
	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{
				ID:            deploymentID,
				EnvironmentID: uuid.New(),
				ServiceConfigSnapshot: serviceconfig.ServiceConfig{
					Name: "svc",
					Dependencies: map[string]serviceconfig.DependencyDeclaration{
						"cache": {Type: "http-service"},
					},
				},
			}, nil
		},
		ListDepConfigsFn: func(_ context.Context, _ uuid.UUID) ([]domain.DependencyDeployment, error) {
			return []domain.DependencyDeployment{
				{
					DependencyName: "cache",
					Status:         domain.DependencyDeploymentStatusPending,
					Managed:        &nonManaged,
					UserConfig:     map[string]any{"url": "http://cache.internal"},
				},
			}, nil
		},
		MarkDepConfigSucceededFn: func(_ context.Context, _ uuid.UUID, _ string, _ json.RawMessage) error {
			markedSucceeded = true
			return nil
		},
	}

	envs := &testutil.MockEnvironmentRepository{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Environment, error) {
			return &domain.Environment{ID: uuid.New(), ClusterID: uuid.New()}, nil
		},
	}

	svc := makeDependencyService(nil, envs, store)
	if err := svc.ScheduleCommands(ctx, deploymentID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !markedSucceeded {
		t.Error("expected non-managed dep to be marked succeeded immediately")
	}
}

func TestScheduleCommands_ManagedDepCreatesCommand(t *testing.T) {
	ctx := context.Background()
	deploymentID := uuid.New()
	clusterID := uuid.New()
	managed := true

	var createdCommand *domain.Command
	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{
				ID:            deploymentID,
				EnvironmentID: uuid.New(),
				ServiceConfigSnapshot: serviceconfig.ServiceConfig{
					Name: "svc",
					Dependencies: map[string]serviceconfig.DependencyDeclaration{
						"db": {Type: "postgres", Config: map[string]any{"version": "14"}},
					},
				},
			}, nil
		},
		ListDepConfigsFn: func(_ context.Context, _ uuid.UUID) ([]domain.DependencyDeployment, error) {
			return []domain.DependencyDeployment{
				{
					DependencyName: "db",
					Status:         domain.DependencyDeploymentStatusPending,
					Managed:        &managed,
					ProviderInputs: map[string]any{},
				},
			}, nil
		},
		CreateCommandFn: func(_ context.Context, cmd *domain.Command) error {
			createdCommand = cmd
			return nil
		},
		SetDepConfigCommandFn: func(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID) error { return nil },
	}

	envs := &testutil.MockEnvironmentRepository{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Environment, error) {
			return &domain.Environment{
				ID:                    uuid.New(),
				ClusterID:             clusterID,
				Name:                  "staging",
				Namespace:             "staging",
				DefaultIngressBaseHost: "example.com",
			}, nil
		},
	}

	svc := makeDependencyService(nil, envs, store)
	if err := svc.ScheduleCommands(ctx, deploymentID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if createdCommand == nil {
		t.Fatal("expected tofu command to be created for managed dep")
	}
	if createdCommand.Type != domain.CommandTypeTofuApply {
		t.Errorf("expected tofu.apply type, got %s", createdCommand.Type)
	}
	if createdCommand.ClusterID != clusterID {
		t.Errorf("expected clusterID %v, got %v", clusterID, createdCommand.ClusterID)
	}
	if createdCommand.Status != domain.CommandStatusQueued {
		t.Errorf("expected queued status, got %s", createdCommand.Status)
	}
}

func TestScheduleCommands_SkipsNonPendingDeps(t *testing.T) {
	ctx := context.Background()
	deploymentID := uuid.New()
	managed := true

	var createCommandCount int
	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{
				ID:            deploymentID,
				EnvironmentID: uuid.New(),
				ServiceConfigSnapshot: serviceconfig.ServiceConfig{
					Name: "svc",
					Dependencies: map[string]serviceconfig.DependencyDeclaration{
						"db": {Type: "postgres"},
					},
				},
			}, nil
		},
		ListDepConfigsFn: func(_ context.Context, _ uuid.UUID) ([]domain.DependencyDeployment, error) {
			return []domain.DependencyDeployment{
				{
					DependencyName: "db",
					Status:         domain.DependencyDeploymentStatusSucceeded, // already done
					Managed:        &managed,
				},
			}, nil
		},
		CreateCommandFn: func(_ context.Context, _ *domain.Command) error {
			createCommandCount++
			return nil
		},
	}

	envs := &testutil.MockEnvironmentRepository{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Environment, error) {
			return &domain.Environment{ID: uuid.New(), ClusterID: uuid.New()}, nil
		},
	}

	svc := makeDependencyService(nil, envs, store)
	if err := svc.ScheduleCommands(ctx, deploymentID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if createCommandCount != 0 {
		t.Errorf("expected no commands for non-pending deps, got %d", createCommandCount)
	}
}

// --- HandleCommandResult ---

func TestHandleCommandResult_SuccessMarksDepSucceeded(t *testing.T) {
	ctx := context.Background()
	cmdID := uuid.New()
	deploymentID := uuid.New()
	output := json.RawMessage(`{"host":"db.internal"}`)

	var markedDepName string
	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn: func(_ context.Context, _ uuid.UUID) (*domain.Command, error) {
			return &domain.Command{
				ID:     cmdID,
				Status: domain.CommandStatusSucceeded,
				Output: output,
			}, nil
		},
		GetDepConfigByCommandIDFn: func(_ context.Context, _ uuid.UUID) (uuid.UUID, *domain.DependencyDeployment, error) {
			return deploymentID, &domain.DependencyDeployment{DependencyName: "db"}, nil
		},
		MarkDepConfigSucceededFn: func(_ context.Context, _ uuid.UUID, depName string, _ json.RawMessage) error {
			markedDepName = depName
			return nil
		},
	}

	svc := makeDependencyService(nil, nil, store)
	if err := svc.HandleCommandResult(ctx, cmdID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if markedDepName != "db" {
		t.Errorf("expected dep 'db' marked succeeded, got %q", markedDepName)
	}
}

func TestHandleCommandResult_FailureMarksDepFailedAndCancelsSiblings(t *testing.T) {
	ctx := context.Background()
	cmdID := uuid.New()
	deploymentID := uuid.New()

	var failedDepName string
	var cancelledFrom, cancelledTo domain.CommandStatus

	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn: func(_ context.Context, _ uuid.UUID) (*domain.Command, error) {
			return &domain.Command{ID: cmdID, Status: domain.CommandStatusFailed}, nil
		},
		GetDepConfigByCommandIDFn: func(_ context.Context, _ uuid.UUID) (uuid.UUID, *domain.DependencyDeployment, error) {
			return deploymentID, &domain.DependencyDeployment{DependencyName: "db"}, nil
		},
		MarkDepConfigFailedFn: func(_ context.Context, _ uuid.UUID, depName string) error {
			failedDepName = depName
			return nil
		},
		UpdateCommandsByDeploymentFn: func(_ context.Context, _ uuid.UUID, from, to domain.CommandStatus) error {
			cancelledFrom = from
			cancelledTo = to
			return nil
		},
	}

	svc := makeDependencyService(nil, nil, store)
	if err := svc.HandleCommandResult(ctx, cmdID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if failedDepName != "db" {
		t.Errorf("expected dep 'db' marked failed, got %q", failedDepName)
	}
	if cancelledFrom != domain.CommandStatusQueued || cancelledTo != domain.CommandStatusCancelled {
		t.Errorf("expected sibling queued→cancelled, got %s→%s", cancelledFrom, cancelledTo)
	}
}

func TestHandleCommandResult_NoDepConfigReturnsNil(t *testing.T) {
	ctx := context.Background()
	cmdID := uuid.New()

	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn: func(_ context.Context, _ uuid.UUID) (*domain.Command, error) {
			return &domain.Command{ID: cmdID, Status: domain.CommandStatusSucceeded}, nil
		},
		GetDepConfigByCommandIDFn: func(_ context.Context, _ uuid.UUID) (uuid.UUID, *domain.DependencyDeployment, error) {
			// Return nil cfg — command not associated with any dep
			return uuid.Nil, nil, nil
		},
	}

	svc := makeDependencyService(nil, nil, store)
	if err := svc.HandleCommandResult(ctx, cmdID); err != nil {
		t.Errorf("expected nil when no dep config found, got %v", err)
	}
}

// --- BuildContexts ---

func TestBuildContexts_UnmarshalsOutputsCorrectly(t *testing.T) {
	svc := &internalDependencyService{}
	rawOutputs := map[string]json.RawMessage{
		"db":    json.RawMessage(`{"host":"db.internal","port":5432}`),
		"cache": json.RawMessage(`{"url":"redis://cache:6379"}`),
	}

	contexts, err := svc.BuildContexts(rawOutputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contexts) != 2 {
		t.Fatalf("expected 2 contexts, got %d", len(contexts))
	}
	if contexts["db"]["host"] != "db.internal" {
		t.Errorf("expected db.host='db.internal', got %v", contexts["db"]["host"])
	}
	if contexts["cache"]["url"] != "redis://cache:6379" {
		t.Errorf("expected cache.url, got %v", contexts["cache"]["url"])
	}
}

func TestBuildContexts_InvalidJSONReturnsError(t *testing.T) {
	svc := &internalDependencyService{}
	rawOutputs := map[string]json.RawMessage{
		"bad": json.RawMessage(`{not valid json}`),
	}
	_, err := svc.BuildContexts(rawOutputs)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestBuildContexts_EmptyInputReturnsEmptyMap(t *testing.T) {
	svc := &internalDependencyService{}
	contexts, err := svc.BuildContexts(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contexts) != 0 {
		t.Errorf("expected empty contexts, got %d entries", len(contexts))
	}
}

// --- Validate ---

func TestValidate_KnownTypePassesValidation(t *testing.T) {
	svc := makeDependencyService(dependency.NewSpecRegistry(), nil, nil)
	err := svc.Validate(context.Background(), map[string]serviceconfig.DependencyDeclaration{
		"db": {Type: "postgres"},
	})
	if err != nil {
		t.Errorf("expected no error for known type, got %v", err)
	}
}

func TestValidate_UnknownTypeReturnsValidationError(t *testing.T) {
	svc := makeDependencyService(dependency.NewSpecRegistry(), nil, nil)
	err := svc.Validate(context.Background(), map[string]serviceconfig.DependencyDeclaration{
		"x": {Type: "nonexistent-type"},
	})
	if err == nil {
		t.Fatal("expected validation error for unknown dep type, got nil")
	}
}

func TestValidate_MixedKnownAndUnknownReportsOnlyUnknown(t *testing.T) {
	svc := makeDependencyService(dependency.NewSpecRegistry(), nil, nil)
	err := svc.Validate(context.Background(), map[string]serviceconfig.DependencyDeclaration{
		"db":  {Type: "postgres"},        // known
		"ext": {Type: "mystery-service"}, // unknown
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

// --- environmentProviderInput ---

func TestEnvironmentProviderInput_IncludesExpectedFields(t *testing.T) {
	env := &domain.Environment{
		Name:                  "staging",
		Namespace:             "staging-ns",
		DefaultIngressBaseHost: "example.com",
	}
	result := environmentProviderInput(env)
	if result["name"] != "staging" {
		t.Errorf("expected name=staging, got %v", result["name"])
	}
	if result["namespace"] != "staging-ns" {
		t.Errorf("expected namespace, got %v", result["namespace"])
	}
	if result["defaultIngressBaseHost"] != "example.com" {
		t.Errorf("expected defaultIngressBaseHost, got %v", result["defaultIngressBaseHost"])
	}
}

