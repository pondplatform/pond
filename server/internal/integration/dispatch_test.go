//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agentconn "github.com/pondplatform/pond/agent/connection"
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

// TestAgent_RestartFailsWhatItWasRunning checks that when the cluster's agent
// connects after a restart, a command it was running before is failed, and
// with it the deployment.
func TestAgent_RestartFailsWhatItWasRunning(t *testing.T) {
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
	if got := field(body, "status"); got != "failed" || !strings.Contains(field(body, "error"), "helm upgrade failed: the agent restarted") {
		t.Fatalf("expected failed because the agent restarted, got %q %q\n%s", got, field(body, "error"), describeDeployment(h.DB, id))
	}
	if cmd := commandRow(t, h, id, cmdID); cmd.Status != "failed" {
		t.Errorf("expected the command failed, got %+v", cmd)
	}
}

// TestAgent_RestartEndsACancellingDeployment checks that a cancelled
// deployment whose agent restarted while running its command stops showing
// as cancelling once the agent connects again.
func TestAgent_RestartEndsACancellingDeployment(t *testing.T) {
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
	if cmd := commandRow(t, h, id, cmdID); cmd.Status != "cancelled" || !strings.Contains(cmd.Error, "the agent restarted") {
		t.Errorf("expected the command cancelled because the agent restarted, got %+v", cmd)
	}
}

// TestAgent_WithoutHelloIsTakenForRestarted checks that an agent from before
// hello, which opens with ready, is taken to hold nothing: what the cluster's
// agent was running is failed.
func TestAgent_WithoutHelloIsTakenForRestarted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	cmdID := fa.WaitForCommands(ctx, t, 1)[0].ID
	fa.Stop()

	conn := agentconn.New(h.WsAddr, scenario.AgentToken)
	if err := conn.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	if err := conn.SendReady(ctx); err != nil {
		t.Fatalf("send ready: %v", err)
	}
	if msg, err := conn.ReceiveMessage(ctx); err != nil || msg.Type != wire.MessageIdle {
		t.Fatalf("expected idle, got %v %v", msg, err)
	}
	if cmd := commandRow(t, h, id, cmdID); cmd.Status != "failed" || !strings.Contains(cmd.Error, "the agent restarted") {
		t.Errorf("expected the command failed because the agent restarted, got %+v", cmd)
	}
}

// TestAgent_ReconnectKeepsTheCommandItRuns checks that an agent that lost its
// connection and comes back running a command keeps it: the command is not
// failed, and its result, sent on the new connection, completes the
// deployment.
func TestAgent_ReconnectKeepsTheCommandItRuns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	cmdID := fa.WaitForCommands(ctx, t, 1)[0].ID
	fa.Stop()

	agent := h.ConnectAgent(ctx, t, scenario.AgentToken, wire.HelloPayload{Running: &cmdID})
	time.Sleep(10 * stopCheckInterval)
	if cmd := commandRow(t, h, id, cmdID); cmd.Status != "dispatched" {
		t.Fatalf("expected the command still dispatched, got %+v", cmd)
	}
	waitForAPIStatus(ctx, t, h, id, "running", time.Second)

	agent.Result(ctx, &wire.ResultPayload{CommandID: cmdID, Success: true})
	if msg := agent.Ready(ctx); msg.Type != wire.MessageIdle {
		t.Fatalf("expected idle, got %s", msg.Type)
	}
	if got := field(waitForTerminal(ctx, t, h, id, 5*time.Second), "status"); got != "succeeded" {
		t.Fatalf("expected succeeded, got %q\n%s", got, describeDeployment(h.DB, id))
	}
}

// TestAgent_ReconnectDeliversAResultFromWhileItWasAway checks that a result
// the agent got while disconnected, named in its hello and sent after it,
// completes the deployment.
func TestAgent_ReconnectDeliversAResultFromWhileItWasAway(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	cmdID := fa.WaitForCommands(ctx, t, 1)[0].ID
	fa.Stop()

	agent := h.ConnectAgent(ctx, t, scenario.AgentToken, wire.HelloPayload{Finished: []uuid.UUID{cmdID}})
	agent.Result(ctx, &wire.ResultPayload{CommandID: cmdID, Success: true})
	if msg := agent.Ready(ctx); msg.Type != wire.MessageIdle {
		t.Fatalf("expected idle, got %s", msg.Type)
	}
	if got := field(waitForTerminal(ctx, t, h, id, 5*time.Second), "status"); got != "succeeded" {
		t.Fatalf("expected succeeded, got %q\n%s", got, describeDeployment(h.DB, id))
	}
}

// TestAgent_ReconnectRequeuesACommandItNeverReceived checks that a command
// handed out on a connection that dropped before the agent got it, so that
// the agent doesn't hold it when it comes back, is queued again and handed
// out next.
func TestAgent_ReconnectRequeuesACommandItNeverReceived(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	cmdID := fa.WaitForCommands(ctx, t, 1)[0].ID
	fa.Stop()

	agent := h.ConnectAgent(ctx, t, scenario.AgentToken, wire.HelloPayload{})
	msg := agent.Ready(ctx)
	var cmd wire.CommandPayload
	if msg.Type != wire.MessageCommand || json.Unmarshal(msg.Data, &cmd) != nil || cmd.ID != cmdID {
		t.Fatalf("expected the command %s handed out again, got %s %s\n%s", cmdID, msg.Type, msg.Data, describeDeployment(h.DB, id))
	}

	agent.Result(ctx, &wire.ResultPayload{CommandID: cmdID, Success: true})
	if msg := agent.Ready(ctx); msg.Type != wire.MessageIdle {
		t.Fatalf("expected idle, got %s", msg.Type)
	}
	if got := field(waitForTerminal(ctx, t, h, id, 5*time.Second), "status"); got != "succeeded" {
		t.Fatalf("expected succeeded, got %q\n%s", got, describeDeployment(h.DB, id))
	}
}

// TestAgent_ReconnectCancelsANeverReceivedCommandOfAnEndedDeployment checks
// that a command the agent never received is not queued again once its
// deployment has ended: it is cancelled, and the deployment stops showing as
// cancelling.
func TestAgent_ReconnectCancelsANeverReceivedCommandOfAnEndedDeployment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	cmdID := fa.WaitForCommands(ctx, t, 1)[0].ID
	fa.Stop()
	if code, resp := cancelDeployment(ctx, t, h, id); code != http.StatusNoContent {
		t.Fatalf("cancel: expected 204, got %d: %s", code, resp)
	}
	waitForAPIStatus(ctx, t, h, id, "cancelling", time.Second)

	agent := h.ConnectAgent(ctx, t, scenario.AgentToken, wire.HelloPayload{})
	if msg := agent.Ready(ctx); msg.Type != wire.MessageIdle {
		t.Fatalf("expected idle, got %s %s", msg.Type, msg.Data)
	}
	waitForAPIStatus(ctx, t, h, id, "cancelled", time.Second)
	if cmd := commandRow(t, h, id, cmdID); cmd.Status != "cancelled" {
		t.Errorf("expected the command cancelled, got %+v", cmd)
	}
}
