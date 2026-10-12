//go:build integration

package integration

import (
	"context"
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

// TestDispatch_TwoAgentsPullDifferentCommands checks that two agents of one
// cluster never get the same command.
func TestDispatch_TwoAgentsPullDifferentCommands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, release := NewGate(t)
	agentA := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})
	agentB := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	idA := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("service-a"), "v1.0.0"))
	idB := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("service-b"), "v1.0.0"))
	cmdA := agentA.WaitForCommands(ctx, t, 1)[0].ID
	cmdB := agentB.WaitForCommands(ctx, t, 1)[0].ID
	if cmdA == cmdB {
		t.Fatalf("both agents got command %s", cmdA)
	}

	release()
	for _, id := range []uuid.UUID{idA, idB} {
		if got := field(waitForTerminal(ctx, t, h, id, 5*time.Second), "status"); got != "succeeded" {
			t.Fatalf("deployment %s: expected succeeded, got %q\n%s", id, got, describeDeployment(h.DB, id))
		}
	}
}

// TestDispatch_RolloutWaitsForItsDependencies checks that the helm command is
// not handed out, even to an idle agent, until every dependency is ready.
func TestDispatch_RolloutWaitsForItsDependencies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, release := NewGate(t)
	busy := TofuOutputBehavior(PostgresOutput)
	busy.Gate = gate
	provisioner := h.StartFakeAgent(ctx, t, scenario.AgentToken, busy)

	req := SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.0")
	req.Dependencies = managedPostgres
	id := submitDeployment(ctx, t, h, req)
	if got := provisioner.WaitForCommands(ctx, t, 1)[0].Type; got != wire.CommandTofuApply {
		t.Fatalf("expected tofu.apply first, got %s", got)
	}

	idle := h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)
	time.Sleep(10 * fakeAgentPollInterval)
	if got := idle.ReceivedCommands(); len(got) != 0 {
		t.Fatalf("expected no command for the idle agent while the dependency is provisioned, got %s", got[0].Type)
	}

	release()
	if got := field(waitForTerminal(ctx, t, h, id, 5*time.Second), "status"); got != "succeeded" {
		t.Fatalf("expected succeeded, got %q\n%s", got, describeDeployment(h.DB, id))
	}
}
