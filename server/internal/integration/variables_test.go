//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
)

// TestDeployment_VariablesReachTheRollout checks that variables survive the
// database: the rollout only starts once the managed dependency is
// provisioned, from the deployment as it was stored.
func TestDeployment_VariablesReachTheRollout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, TofuOutputBehavior(PostgresOutput))

	cfg := ServiceConfigWithDep("test-service")
	cfg.Variables = map[string]serviceconfig.VariableDeclaration{"SIGNING_KEY": {Secret: true}, "PUBLIC_NAME": {}}
	cfg.Env = map[string]string{
		"SIGNING_KEY": "{{variables.SIGNING_KEY}}",
		"DB_HOST":     "{{dependencies.postgres.host}}",
		"WHERE":       "{{service.name}} in {{environment.name}}",
	}
	cfg = WithConfigValues(cfg, map[string]any{"name": "{{variables.PUBLIC_NAME}}"})
	req := SubmitRequest(scenario, cfg, "v1.0.0")
	req.Dependencies = managedPostgres
	req.Variables = map[string]string{"SIGNING_KEY": "s3cr3t", "PUBLIC_NAME": "Acme"}
	id := submitDeployment(ctx, t, h, req)

	body := waitForTerminal(ctx, t, h, id, 5*time.Second)
	if got := field(body, "status"); got != "succeeded" {
		t.Fatalf("expected status succeeded, got %q (error %q)\n%s", got, field(body, "error"), describeDeployment(h.DB, id))
	}

	var stored []byte
	if err := h.DB.QueryRowContext(ctx, `SELECT variables FROM deployments WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("read variables: %v", err)
	}
	var variables map[string]string
	if err := json.Unmarshal(stored, &variables); err != nil || !maps.Equal(variables, req.Variables) {
		t.Errorf("expected the variables stored as %v, got %s (%v)", req.Variables, stored, err)
	}

	values := helmUpgradeValues(t, fa.ReceivedCommands())
	wantEnv := map[string]string{"SIGNING_KEY": "s3cr3t", "DB_HOST": "db.test.local", "WHERE": "test-service in test-env"}
	if !maps.Equal(values.Env, wantEnv) {
		t.Errorf("expected env %v, got %v", wantEnv, values.Env)
	}

	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "s3cr3t") {
		t.Errorf("the deployment response carries a variable's value: %s", raw)
	}
}

// TestDeployment_UndeclaredVariableIsRejected checks that a value for a
// variable pond.yml doesn't declare is refused, and nothing is written.
func TestDeployment_UndeclaredVariableIsRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)

	req := SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0")
	req.Variables = map[string]string{"SIGNING_KEY": "s3cr3t"}
	_, err := h.Client().SubmitDeployment(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "status 422") || !strings.Contains(err.Error(), "not declared in pond.yml") {
		t.Fatalf("expected a 422 for the undeclared variable, got %v", err)
	}
	if n := countDeployments(t, h, scenario.ProjectID); n != 0 {
		t.Errorf("expected no deployment to be written, got %d", n)
	}
}

func helmUpgradeValues(t *testing.T, commands []*wire.CommandPayload) helmgen.HelmValues {
	t.Helper()
	for _, cmd := range commands {
		if cmd.Type != wire.CommandHelmUpgrade {
			continue
		}
		var payload wire.HelmUpgradePayload
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			t.Fatalf("decode helm.upgrade payload: %v", err)
		}
		var values helmgen.HelmValues
		if err := yaml.Unmarshal(payload.Values, &values); err != nil {
			t.Fatalf("decode helm values %s: %v", payload.Values, err)
		}
		return values
	}
	t.Fatalf("no helm.upgrade among %d commands", len(commands))
	return helmgen.HelmValues{}
}
