package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

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

func TestApplyInputs_ReturnsOnlyTheNamedDependenciesConfigured(t *testing.T) {
	deps := []db.DependencyDeployment{
		{DependencyName: "cache", Status: db.DependencyDeploymentStatusAwaitingInput, UserConfig: map[string]any{}},
		{DependencyName: "db", Status: db.DependencyDeploymentStatusAwaitingInput},
	}

	got := applyInputs(deps, map[string]api.DependencyInput{"db": {Managed: true}})

	if len(got) != 1 || got[0].DependencyName != "db" {
		t.Fatalf("expected only db, got %+v", got)
	}
	if !isManaged(got[0]) || got[0].Status != db.DependencyDeploymentStatusPending {
		t.Errorf("expected db managed and pending, got %+v", got[0])
	}
	if got[0].ProviderInputs == nil {
		t.Error("expected nil values to become an empty map")
	}
	if deps[1].Managed != nil {
		t.Error("applyInputs must not modify its input")
	}
}

func TestApplyInput_KeepsTheCommandOnlyWhileProvisioning(t *testing.T) {
	cmdID := uuid.New()
	input := api.DependencyInput{Managed: true}

	pending := applyInput(db.DependencyDeployment{Status: db.DependencyDeploymentStatusPending, CommandID: &cmdID}, input)
	if pending.CommandID == nil || *pending.CommandID != cmdID {
		t.Errorf("a dependency being provisioned must keep its command, got %v", pending.CommandID)
	}

	succeeded := applyInput(db.DependencyDeployment{
		Status:    db.DependencyDeploymentStatusSucceeded,
		CommandID: &cmdID,
		Output:    json.RawMessage(`{"host":"db"}`),
	}, input)
	if succeeded.CommandID != nil || succeeded.Output != nil || succeeded.Status != db.DependencyDeploymentStatusPending {
		t.Errorf("a provisioned dependency must be provisioned afresh, got %+v", succeeded)
	}
}
