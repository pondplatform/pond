package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/dependency"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

func makeDependencyService(envs *testutil.MockEnvironmentRepository, store *testutil.MockDeploymentInfoStore, topics *testutil.MockTopics) *internalDependencyService {
	if envs == nil {
		envs = &testutil.MockEnvironmentRepository{}
	}
	if store == nil {
		store = &testutil.MockDeploymentInfoStore{}
	}
	if topics == nil {
		topics = &testutil.MockTopics{}
	}
	return &internalDependencyService{
		specs:          dependency.NewSpecRegistry(),
		envs:           envs,
		deploymentInfo: store,
		tx:             inlineTx(store),
		topics:         topics,
	}
}

func ptr[T any](v T) *T { return &v }

// --- aggregateStatus / initialStatus ---

func TestAggregateStatus(t *testing.T) {
	const (
		awaiting  = db.DependencyDeploymentStatusAwaitingInput
		pending   = db.DependencyDeploymentStatusPending
		succeeded = db.DependencyDeploymentStatusSucceeded
		failed    = db.DependencyDeploymentStatusFailed
	)
	cases := map[string]struct {
		statuses []db.DependencyDeploymentStatus
		want     db.DependencyDeploymentStatus
	}{
		"failed wins over all":        {[]db.DependencyDeploymentStatus{succeeded, failed, awaiting, pending}, failed},
		"awaiting input over pending": {[]db.DependencyDeploymentStatus{pending, awaiting}, awaiting},
		"pending over succeeded":      {[]db.DependencyDeploymentStatus{succeeded, pending}, pending},
		"all succeeded":               {[]db.DependencyDeploymentStatus{succeeded, succeeded}, succeeded},
		"no dependencies":             {nil, succeeded},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			deps := make([]db.DependencyDeployment, len(tc.statuses))
			for i, s := range tc.statuses {
				deps[i].Status = s
			}
			if got := aggregateStatus(deps); got != tc.want {
				t.Errorf("expected %s, got %s", tc.want, got)
			}
		})
	}
}

func TestInitialStatus(t *testing.T) {
	awaiting := []db.DependencyDeployment{{Status: db.DependencyDeploymentStatusPending}, {Status: db.DependencyDeploymentStatusAwaitingInput}}
	if got := initialStatus(awaiting); got != api.DeploymentStatusAwaitingInput {
		t.Errorf("expected awaiting_input when a dependency awaits input, got %s", got)
	}
	if got := initialStatus([]db.DependencyDeployment{{Status: db.DependencyDeploymentStatusPending}}); got != api.DeploymentStatusPending {
		t.Errorf("expected pending, got %s", got)
	}
	if got := initialStatus(nil); got != api.DeploymentStatusPending {
		t.Errorf("expected pending without dependencies, got %s", got)
	}
}

// --- newDependencyDeployments ---

func deploymentWithDeps(deps map[string]serviceconfig.DependencyDeclaration) db.Deployment {
	return db.Deployment{
		ID:                    uuid.New(),
		ServiceConfigSnapshot: serviceconfig.ServiceConfig{Name: "my-svc", Dependencies: deps},
	}
}

func TestNewDependencyDeployments_FirstTimeAwaitsInput(t *testing.T) {
	deployment := deploymentWithDeps(map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}})

	deps := newDependencyDeployments(deployment, nil, nil)

	if len(deps) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(deps))
	}
	d := deps[0]
	if d.DeploymentId != deployment.ID || d.DependencyName != "db" || d.DependencyType != "postgres" {
		t.Errorf("unexpected identity: %+v", d)
	}
	if d.Status != db.DependencyDeploymentStatusAwaitingInput || d.Managed != nil {
		t.Errorf("expected awaiting_input with no managed flag, got status=%s managed=%v", d.Status, d.Managed)
	}
}

func TestNewDependencyDeployments_AppliesInputs(t *testing.T) {
	deployment := deploymentWithDeps(map[string]serviceconfig.DependencyDeclaration{
		"db":  {Type: "postgres"},
		"api": {Type: "http-service"},
	})
	inputs := map[string]api.DependencyInput{
		"db":  {Managed: true, Values: map[string]any{"instances": 1}},
		"api": {Managed: false, Values: map[string]any{"url": "https://api.example.com"}},
	}

	deps := newDependencyDeployments(deployment, inputs, nil)

	if len(deps) != 2 || deps[0].DependencyName != "api" || deps[1].DependencyName != "db" {
		t.Fatalf("expected api then db, got %+v", deps)
	}
	apiDep, dbDep := deps[0], deps[1]
	if !isManaged(dbDep) || dbDep.Status != db.DependencyDeploymentStatusPending {
		t.Errorf("expected managed pending db, got %+v", dbDep)
	}
	if dbDep.ProviderInputs["instances"] != 1 || len(dbDep.UserConfig) != 0 {
		t.Errorf("expected managed values in ProviderInputs only, got provider=%v user=%v", dbDep.ProviderInputs, dbDep.UserConfig)
	}
	if apiDep.Managed == nil || *apiDep.Managed || apiDep.Status != db.DependencyDeploymentStatusPending {
		t.Errorf("expected unmanaged pending api, got %+v", apiDep)
	}
	if apiDep.UserConfig["url"] != "https://api.example.com" || len(apiDep.ProviderInputs) != 0 {
		t.Errorf("expected unmanaged values in UserConfig only, got provider=%v user=%v", apiDep.ProviderInputs, apiDep.UserConfig)
	}
}

func TestNewDependencyDeployments_CarriesOverPreviousConfig(t *testing.T) {
	deployment := deploymentWithDeps(map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}})
	previous := map[string]db.DependencyDeployment{"db": {
		ID:             uuid.New(),
		DependencyName: "db",
		Managed:        ptr(true),
		ProviderInputs: map[string]any{"region": "eu-west-1"},
		UserConfig:     map[string]any{"version": "14"},
		Output:         json.RawMessage(`{"host":"db.example.com"}`),
		Status:         db.DependencyDeploymentStatusSucceeded,
		CommandID:      ptr(uuid.New()),
	}}

	d := newDependencyDeployments(deployment, nil, previous)[0]

	if !isManaged(d) || d.Status != db.DependencyDeploymentStatusPending {
		t.Errorf("expected managed pending, got %+v", d)
	}
	if d.ProviderInputs["region"] != "eu-west-1" || d.UserConfig["version"] != "14" {
		t.Errorf("expected inputs carried over, got provider=%v user=%v", d.ProviderInputs, d.UserConfig)
	}
	if d.ID == previous["db"].ID || d.DeploymentId != deployment.ID || d.CommandID != nil || d.Output != nil {
		t.Errorf("expected a fresh row for the new deployment, got %+v", d)
	}
}

func TestNewDependencyDeployments_PreviousWithoutManagedFlagAwaitsInput(t *testing.T) {
	deployment := deploymentWithDeps(map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}})
	previous := map[string]db.DependencyDeployment{"db": {DependencyName: "db", Status: db.DependencyDeploymentStatusAwaitingInput}}

	d := newDependencyDeployments(deployment, nil, previous)[0]

	if d.Status != db.DependencyDeploymentStatusAwaitingInput {
		t.Errorf("expected awaiting_input when the previous deployment was never configured, got %s", d.Status)
	}
}

func TestNewDependencyDeployments_InputOverridesPreviousConfig(t *testing.T) {
	deployment := deploymentWithDeps(map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}})
	previous := map[string]db.DependencyDeployment{"db": {DependencyName: "db", Managed: ptr(true), ProviderInputs: map[string]any{"instances": 3}}}
	inputs := map[string]api.DependencyInput{"db": {Managed: false, Values: map[string]any{"host": "db.internal"}}}

	d := newDependencyDeployments(deployment, inputs, previous)[0]

	if d.Managed == nil || *d.Managed || d.UserConfig["host"] != "db.internal" || len(d.ProviderInputs) != 0 {
		t.Errorf("expected unmanaged config from input only, got %+v", d)
	}
}

func TestApplyInputs_ConfiguresOnlyNamedDependencies(t *testing.T) {
	deps := []db.DependencyDeployment{
		{DependencyName: "cache", Status: db.DependencyDeploymentStatusAwaitingInput, UserConfig: map[string]any{}},
		{DependencyName: "db", Status: db.DependencyDeploymentStatusAwaitingInput, CommandID: ptr(uuid.New())},
	}

	got := applyInputs(deps, map[string]api.DependencyInput{"db": {Managed: true}})

	if got[0].Status != db.DependencyDeploymentStatusAwaitingInput || got[0].Managed != nil {
		t.Errorf("expected cache unchanged, got %+v", got[0])
	}
	if !isManaged(got[1]) || got[1].Status != db.DependencyDeploymentStatusPending || got[1].CommandID != deps[1].CommandID {
		t.Errorf("expected db managed pending with its command kept, got %+v", got[1])
	}
	if got[1].ProviderInputs == nil {
		t.Error("expected nil values to become an empty map")
	}
	if deps[1].Managed != nil {
		t.Error("applyInputs must not modify its input")
	}
}

// --- ScheduleCommand ---

func TestScheduleCommand_UnmanagedIsMarkedSucceededWithItsConfig(t *testing.T) {
	deploymentID := uuid.New()
	var markedName string
	var markedOutput json.RawMessage
	store := &testutil.MockDeploymentInfoStore{
		MarkDepConfigSucceededFn: func(_ context.Context, id uuid.UUID, name string, output json.RawMessage) error {
			if id != deploymentID {
				t.Errorf("expected deployment %v, got %v", deploymentID, id)
			}
			markedName, markedOutput = name, output
			return nil
		},
		CreateCommandFn: func(context.Context, *db.Command) error {
			t.Error("no command should be created for an unmanaged dependency")
			return nil
		},
	}

	svc := makeDependencyService(nil, store, nil)
	err := svc.ScheduleCommand(context.Background(), db.Deployment{ID: deploymentID}, db.DependencyDeployment{
		DeploymentId:   deploymentID,
		DependencyName: "cache",
		Managed:        ptr(false),
		UserConfig:     map[string]any{"url": "http://cache.internal"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if markedName != "cache" || string(markedOutput) != `{"url":"http://cache.internal"}` {
		t.Errorf("expected cache marked succeeded with its user config, got %q %s", markedName, markedOutput)
	}
}

func TestScheduleCommand_ManagedCreatesLinksThenPublishes(t *testing.T) {
	deploymentID := uuid.New()
	clusterID := uuid.New()

	var calls []string
	var created *db.Command
	store := &testutil.MockDeploymentInfoStore{
		CreateCommandFn: func(_ context.Context, cmd *db.Command) error {
			calls = append(calls, "create")
			created = cmd
			return nil
		},
		SetDepConfigCommandFn: func(_ context.Context, id uuid.UUID, name string, cmdID uuid.UUID) error {
			calls = append(calls, "link")
			if id != deploymentID || name != "db" || cmdID != created.ID {
				t.Errorf("expected db of %v linked to %v, got %s of %v linked to %v", deploymentID, created.ID, name, id, cmdID)
			}
			return nil
		},
	}
	envs := &testutil.MockEnvironmentRepository{
		GetByIDFn: func(context.Context, uuid.UUID) (*db.Environment, error) {
			return &db.Environment{ClusterID: clusterID, Name: "staging", Namespace: "staging"}, nil
		},
	}
	topics := &testutil.MockTopics{}
	_, _ = topics.CommandQueued.For(clusterID).Subscribe(context.Background(), func(context.Context, events.CommandQueued) {
		calls = append(calls, "publish")
	})

	deployment := db.Deployment{
		ID: deploymentID,
		ServiceConfigSnapshot: serviceconfig.ServiceConfig{
			Name:         "svc",
			Dependencies: map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}},
		},
	}
	svc := makeDependencyService(envs, store, topics)
	err := svc.ScheduleCommand(context.Background(), deployment, db.DependencyDeployment{DependencyName: "db", Managed: ptr(true)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(calls, []string{"create", "link", "publish"}) {
		t.Errorf("expected create, link, publish; got %v", calls)
	}
	if created.Type != db.CommandTypeTofuApply || created.ClusterID != clusterID || created.DeploymentID != deploymentID || created.Status != db.CommandStatusQueued {
		t.Errorf("unexpected command: %+v", created)
	}
}

func TestScheduleCommand_CreateErrorPublishesNothing(t *testing.T) {
	clusterID := uuid.New()
	createErr := errors.New("insert failed")
	store := &testutil.MockDeploymentInfoStore{
		CreateCommandFn: func(context.Context, *db.Command) error { return createErr },
		SetDepConfigCommandFn: func(context.Context, uuid.UUID, string, uuid.UUID) error {
			t.Error("a command that was not created must not be linked")
			return nil
		},
	}
	envs := &testutil.MockEnvironmentRepository{
		GetByIDFn: func(context.Context, uuid.UUID) (*db.Environment, error) {
			return &db.Environment{ClusterID: clusterID}, nil
		},
	}
	topics := &testutil.MockTopics{}

	svc := makeDependencyService(envs, store, topics)
	err := svc.ScheduleCommand(context.Background(), db.Deployment{}, db.DependencyDeployment{DependencyName: "db", Managed: ptr(true)})
	if !errors.Is(err, createErr) {
		t.Fatalf("expected create error, got %v", err)
	}
	if n := topics.CommandQueued.PublishedCount(); n != 0 {
		t.Errorf("expected no wake for an uncreated command, got %d", n)
	}
}

func TestScheduleCommand_PublishErrorPropagates(t *testing.T) {
	clusterID := uuid.New()
	publishErr := errors.New("broker down")
	envs := &testutil.MockEnvironmentRepository{
		GetByIDFn: func(context.Context, uuid.UUID) (*db.Environment, error) {
			return &db.Environment{ClusterID: clusterID}, nil
		},
	}
	topics := &testutil.MockTopics{}
	topics.CommandQueued.For(clusterID).PublishErr = publishErr

	svc := makeDependencyService(envs, nil, topics)
	err := svc.ScheduleCommand(context.Background(), db.Deployment{}, db.DependencyDeployment{DependencyName: "db", Managed: ptr(true)})
	if !errors.Is(err, publishErr) {
		t.Fatalf("expected publish error, got %v", err)
	}
}

func TestScheduleCommand_MissingManagedFlagIsAnError(t *testing.T) {
	svc := makeDependencyService(nil, nil, nil)
	if err := svc.ScheduleCommand(context.Background(), db.Deployment{}, db.DependencyDeployment{DependencyName: "db"}); err == nil {
		t.Fatal("expected an error for a pending dependency without a managed flag")
	}
}

// --- buildContexts ---

func TestBuildContexts_UnmarshalsOutputsCorrectly(t *testing.T) {
	rawOutputs := map[string]json.RawMessage{
		"db":    json.RawMessage(`{"host":"db.internal","port":5432}`),
		"cache": json.RawMessage(`{"url":"redis://cache:6379"}`),
	}

	contexts, err := buildContexts(rawOutputs)
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
	_, err := buildContexts(map[string]json.RawMessage{"bad": json.RawMessage(`{not valid json}`)})
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestBuildContexts_EmptyInputReturnsEmptyMap(t *testing.T) {
	contexts, err := buildContexts(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contexts) != 0 {
		t.Errorf("expected empty contexts, got %d entries", len(contexts))
	}
}

// --- Validate ---

func TestValidate_KnownTypePassesValidation(t *testing.T) {
	svc := makeDependencyService(nil, nil, nil)
	err := svc.Validate(context.Background(), map[string]serviceconfig.DependencyDeclaration{
		"db": {Type: "postgres"},
	})
	if err != nil {
		t.Errorf("expected no error for known type, got %v", err)
	}
}

func TestValidate_UnknownTypeReturnsValidationError(t *testing.T) {
	svc := makeDependencyService(nil, nil, nil)
	err := svc.Validate(context.Background(), map[string]serviceconfig.DependencyDeclaration{
		"x": {Type: "nonexistent-type"},
	})
	if err == nil {
		t.Fatal("expected validation error for unknown dep type, got nil")
	}
}

func TestValidate_MixedKnownAndUnknownReportsOnlyUnknown(t *testing.T) {
	svc := makeDependencyService(nil, nil, nil)
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
	env := &db.Environment{
		Name:                   "staging",
		Namespace:              "staging-ns",
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
