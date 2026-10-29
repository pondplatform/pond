//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	shared "github.com/pondplatform/pond/shared/server/api"
)

// TestSubmit_RefusedWhileADeploymentIsInProgress checks that a service has at
// most one deployment in progress per environment. Another deploy there is
// refused with 409, and a deploy to another environment is not. Once the
// first is cancelled, the next one is accepted while the agent still runs
// the first's command, and its own command waits for that one.
func TestSubmit_RefusedWhileADeploymentIsInProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, release := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate, IgnoreCancel: true})

	first := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("api"), "v1.0.0"))
	running := fa.WaitForCommands(ctx, t, 1)[0].ID

	next := SubmitRequest(scenario, MinimalServiceConfig("api"), "v1.0.1")
	_, err := h.Client().SubmitDeployment(ctx, next)
	if err == nil || !strings.Contains(err.Error(), "409") ||
		!strings.Contains(err.Error(), "deployment "+first.String()+` of service \"api\" to environment \"test-env\" is in progress`) {
		t.Fatalf("expected the second deploy to be refused, naming the first, got %v", err)
	}
	if n := countDeployments(t, h, scenario.ProjectID); n != 1 {
		t.Errorf("expected only the first deployment to be written, got %d", n)
	}
	// The unique index backs the check up.
	duplicate := db.Deployment{
		ID:                    uuid.New(),
		ServiceID:             serviceNamed(ctx, t, h, scenario.ProjectID, "api").ID,
		EnvironmentID:         scenario.EnvID,
		Kind:                  db.DeploymentKindDeploy,
		ImageTag:              "v1.0.1",
		ServiceConfigSnapshot: MinimalServiceConfig("api"),
		Status:                db.DeploymentInProgress,
		CreatedAt:             time.Now(),
	}
	if err := store.NewDeploymentInfoStore(h.DB).Create(ctx, &duplicate); !errors.Is(err, shared.ErrConflict) {
		t.Fatalf("expected the store to refuse a second deployment in progress with a conflict, got %v", err)
	}

	secondEnvironment(ctx, t, h, scenario)
	elsewhere := next
	elsewhere.EnvironmentName = "test-env-2"
	submitDeployment(ctx, t, h, elsewhere)

	if code, resp := cancelDeployment(ctx, t, h, first); code != http.StatusNoContent {
		t.Fatalf("cancel: expected 204, got %d: %s", code, resp)
	}
	waitForCancel(t, fa, running)
	waitForAPIStatus(ctx, t, h, first, "cancelling", time.Second)
	second := submitDeployment(ctx, t, h, next)
	time.Sleep(5 * fakeAgentPollInterval)
	if got := fa.ReceivedCommands(); len(got) != 1 {
		t.Fatalf("expected the second deployment's command to wait for the first's, got %d commands", len(got))
	}

	release()
	waitForAPIStatus(ctx, t, h, first, "cancelled", 2*time.Second)
	waitForSucceeded(ctx, t, h, second)
}
