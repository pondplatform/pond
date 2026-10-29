//go:build integration

package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/agent/wire"
)

// waitForAPIStatus fails the test unless the deployment's API status becomes
// status within timeout.
func waitForAPIStatus(ctx context.Context, t *testing.T, h *TestHarness, id uuid.UUID, status string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		d, err := h.Client().GetDeployment(ctx, id)
		if err != nil {
			t.Fatalf("get deployment %s: %v", id, err)
		}
		if string(d.Status) == status {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected deployment %s to be %q within %s, still %q\n%s", id, status, timeout, d.Status, describeDeployment(h.DB, id))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestDispatch_RunningOnceAnAgentPicksItUp checks that a deployment shows as
// running while an agent works on it.
func TestDispatch_RunningOnceAnAgentPicksItUp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, release := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	fa.WaitForCommands(ctx, t, 1)
	waitForAPIStatus(ctx, t, h, id, "running", 2*time.Second)

	release()
	if got := field(waitForTerminal(ctx, t, h, id, 5*time.Second), "status"); got != "succeeded" {
		t.Fatalf("expected succeeded, got %q\n%s", got, describeDeployment(h.DB, id))
	}
}

// TestDispatch_RolloutWaitsForItsDependencies checks that the helm command is
// not queued until every dependency is ready.
func TestDispatch_RolloutWaitsForItsDependencies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, release := NewGate(t)
	busy := TofuOutputBehavior(PostgresOutput)
	busy.Gate = gate
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, busy)

	req := SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.0")
	req.Dependencies = managedPostgres
	id := submitDeployment(ctx, t, h, req)
	if got := fa.WaitForCommands(ctx, t, 1)[0].Type; got != wire.CommandTofuApply {
		t.Fatalf("expected tofu.apply first, got %s", got)
	}
	if n := countCommands(t, h.DB, id, wire.CommandHelmUpgrade); n != 0 {
		t.Fatalf("expected no helm.upgrade while the dependency is provisioned, got %d", n)
	}

	release()
	if got := field(waitForTerminal(ctx, t, h, id, 5*time.Second), "status"); got != "succeeded" {
		t.Fatalf("expected succeeded, got %q\n%s", got, describeDeployment(h.DB, id))
	}
}

// TestAgent_ReconnectFailsWhatItWasRunning checks that when the cluster's
// agent connects, a command it was running before is failed, and with it the
// deployment.
func TestAgent_ReconnectFailsWhatItWasRunning(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	cmdID := fa.WaitForCommands(ctx, t, 1)[0].ID
	fa.Stop()
	h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)

	body := waitForTerminal(ctx, t, h, id, 5*time.Second)
	if got := field(body, "status"); got != "failed" || !strings.Contains(field(body, "error"), "helm upgrade failed: the agent disconnected") {
		t.Fatalf("expected failed because the agent disconnected, got %q %q\n%s", got, field(body, "error"), describeDeployment(h.DB, id))
	}
	if cmd := commandRow(t, h, id, cmdID); cmd.Status != "failed" {
		t.Errorf("expected the command failed, got %+v", cmd)
	}
}

// TestAgent_ReconnectEndsACancellingDeployment checks that a cancelled
// deployment whose agent disconnected while running its command stops
// showing as cancelling once the agent connects again.
func TestAgent_ReconnectEndsACancellingDeployment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate, IgnoreCancel: true})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	cmdID := fa.WaitForCommands(ctx, t, 1)[0].ID
	if code, resp := cancelDeployment(ctx, t, h, id); code != http.StatusNoContent {
		t.Fatalf("cancel: expected 204, got %d: %s", code, resp)
	}
	waitForCancel(t, fa, cmdID)
	waitForAPIStatus(ctx, t, h, id, "cancelling", time.Second)
	fa.Stop()
	h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)

	waitForAPIStatus(ctx, t, h, id, "cancelled", 2*time.Second)
	if cmd := commandRow(t, h, id, cmdID); cmd.Status != "cancelled" || !strings.Contains(cmd.Error, "the agent disconnected") {
		t.Errorf("expected the command cancelled because the agent disconnected, got %+v", cmd)
	}
}
