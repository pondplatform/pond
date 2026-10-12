//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	shared "github.com/pondplatform/pond/shared/server/api"
)

// providerInputs reads the input a deployment resolved for one dependency.
func providerInputs(t *testing.T, h *TestHarness, id uuid.UUID, dependency string) map[string]any {
	t.Helper()
	var raw []byte
	if err := h.DB.QueryRow(
		`SELECT provider_inputs FROM dependency_deployments WHERE deployment_id = $1 AND dependency_name = $2`,
		id, dependency,
	).Scan(&raw); err != nil {
		t.Fatalf("query provider inputs of %s: %v", dependency, err)
	}
	var inputs map[string]any
	if err := json.Unmarshal(raw, &inputs); err != nil {
		t.Fatalf("decode provider inputs: %v", err)
	}
	return inputs
}

func countDeployments(t *testing.T, h *TestHarness, projectID uuid.UUID) int {
	t.Helper()
	var n int
	if err := h.DB.QueryRow(
		`SELECT count(*) FROM deployments d JOIN services s ON s.id = d.service_id WHERE s.project_id = $1`, projectID,
	).Scan(&n); err != nil {
		t.Fatalf("count deployments: %v", err)
	}
	return n
}

// TestSubmit_RequestInputOverridesThePreviousInput checks that input given on
// submit replaces the previous deployment's input for that dependency.
func TestSubmit_RequestInputOverridesThePreviousInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	h.StartFakeAgent(ctx, t, scenario.AgentToken, TofuOutputBehavior(PostgresOutput))

	first := SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.0")
	first.Dependencies = managedPostgres
	firstID := submitDeployment(ctx, t, h, first)
	waitForTerminal(ctx, t, h, firstID, 5*time.Second)

	second := SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.1")
	second.Dependencies = map[string]shared.DependencyInput{"postgres": {Managed: true, Values: map[string]any{"instances": 3}}}
	secondID := submitDeployment(ctx, t, h, second)

	if got := providerInputs(t, h, secondID, "postgres")["instances"]; got != float64(3) {
		t.Errorf("expected the requested instances=3, got %v", got)
	}
}

// TestSubmit_NoInputAndNothingToReuseIsRejected checks that a first
// deployment without input for its dependency is refused, and writes nothing.
func TestSubmit_NoInputAndNothingToReuseIsRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)

	_, err := h.Client().SubmitDeployment(ctx, SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.0"))
	if err == nil || !strings.Contains(err.Error(), "status 422") || !strings.Contains(err.Error(), "missing input") {
		t.Fatalf("expected a 422 for the missing input, got %v", err)
	}
	if n := countDeployments(t, h, scenario.ProjectID); n != 0 {
		t.Errorf("expected no deployment to be written, got %d", n)
	}
}

// TestSubmit_UnrenderableConfigIsRejected checks that a config that cannot be
// rendered from an unmanaged dependency's values is refused at submit, since
// nothing has to run before it is known.
func TestSubmit_UnrenderableConfigIsRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)

	cfg := WithConfigValues(ServiceConfigWithDeps("test-service", map[string]string{"api": "http-service"}), map[string]any{
		"API_TOKEN": "{{api.token}}",
	})
	req := SubmitRequest(scenario, cfg, "v1.0.0")
	req.Dependencies = map[string]shared.DependencyInput{"api": {Managed: false, Values: map[string]any{"url": "http://api"}}}

	_, err := h.Client().SubmitDeployment(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "status 422") || !strings.Contains(err.Error(), "api.token") {
		t.Fatalf("expected a 422 naming api.token, got %v", err)
	}
	if n := countDeployments(t, h, scenario.ProjectID); n != 0 {
		t.Errorf("expected no deployment to be written, got %d", n)
	}
}
