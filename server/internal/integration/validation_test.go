//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	shared "github.com/pondplatform/pond/shared/server/api"
)

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

// TestSubmit_NoInputIsRejected checks that a deployment without input for its
// dependency is refused, and writes nothing.
func TestSubmit_NoInputIsRejected(t *testing.T) {
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

// TestSubmit_PreviousInputIsNotReused checks that a submit without input is
// refused even when the service's previous deployment had input.
func TestSubmit_PreviousInputIsNotReused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	h.StartFakeAgent(ctx, t, scenario.AgentToken, TofuOutputBehavior(PostgresOutput))

	first := SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.0")
	first.Dependencies = managedPostgres
	firstID := submitDeployment(ctx, t, h, first)
	if got := field(waitForTerminal(ctx, t, h, firstID, 5*time.Second), "status"); got != "succeeded" {
		t.Fatalf("first deployment: expected succeeded, got %q\n%s", got, describeDeployment(h.DB, firstID))
	}

	_, err := h.Client().SubmitDeployment(ctx, SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.1"))
	if err == nil || !strings.Contains(err.Error(), "status 422") || !strings.Contains(err.Error(), "missing input") {
		t.Fatalf("expected a 422 for the missing input, got %v", err)
	}
	if n := countDeployments(t, h, scenario.ProjectID); n != 1 {
		t.Errorf("expected only the first deployment to be written, got %d", n)
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
