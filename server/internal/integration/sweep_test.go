//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pondplatform/pond/shared/agent/wire"
)

// TestSweep_TimesOutACommandWithoutAResult checks that a command whose agent
// never reports back fails the deployment with a reason, instead of leaving it
// pending.
func TestSweep_TimesOutACommandWithoutAResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarnessWithTiming(t, testConnStr, testAMQPURL, FastTiming)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	cmdID := fa.WaitForCommands(ctx, t, 1)[0].ID

	body := waitForTerminal(ctx, t, h, id, 5*time.Second)
	if got := field(body, "status"); got != "failed" {
		t.Fatalf("expected failed, got %q\n%s", got, describeDeployment(h.DB, id))
	}
	if errMsg := field(body, "error"); !strings.Contains(errMsg, "did not report a result") {
		t.Errorf("expected the error to say the agent did not report a result, got %q", errMsg)
	}
	waitForCommandStatus(t, h, id, cmdID, "failed", time.Second)
}

// TestSweep_FailsADeploymentThatCannotAdvance checks that a deployment the
// server cannot move forward fails with a reason once the stall timeout
// passes, instead of staying pending.
func TestSweep_FailsADeploymentThatCannotAdvance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarnessWithTiming(t, testConnStr, testAMQPURL, FastTiming)
	scenario := BuildScenario(ctx, t, h)
	failCommandInserts(t, h.DB, scenario.ClusterID, wire.CommandHelmUpgrade)

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))

	body := waitForTerminal(ctx, t, h, id, FastTiming.OrphanTimeout+3*time.Second)
	if got := field(body, "status"); got != "failed" {
		t.Fatalf("expected failed, got %q\n%s", got, describeDeployment(h.DB, id))
	}
	if errMsg := field(body, "error"); !strings.Contains(errMsg, "could not advance the deployment") {
		t.Errorf("expected the error to say the server could not advance it, got %q", errMsg)
	}
}
