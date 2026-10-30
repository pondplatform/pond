//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/agent/wire"
	shared "github.com/pondplatform/pond/shared/server/api"
)

func commandRow(t *testing.T, h *TestHarness, deploymentID, cmdID uuid.UUID) cmdRow {
	t.Helper()
	for _, cmd := range commandsOf(t, h.DB, deploymentID) {
		if cmd.ID == cmdID {
			return cmd
		}
	}
	t.Fatalf("command %s not found\n%s", cmdID, describeDeployment(h.DB, deploymentID))
	return cmdRow{}
}

// waitForCancel fails the test unless the agent is told to cancel the
// command within a second.
func waitForCancel(t *testing.T, fa *FakeAgent, cmdID uuid.UUID) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !slices.Contains(fa.CancelledCommands(), cmdID) {
		if time.Now().After(deadline) {
			t.Fatalf("expected the agent to be told to cancel %s", cmdID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCancel_StopsTheRunningCommand checks that cancelling a deployment tells
// the agent to stop the command it is running.
func TestCancel_StopsTheRunningCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	cmdID := fa.WaitForCommands(ctx, t, 1)[0].ID
	if code, resp := cancelDeployment(ctx, t, h, id); code != http.StatusNoContent {
		t.Fatalf("cancel: expected 204, got %d: %s", code, resp)
	}

	waitForCancel(t, fa, cmdID)
	waitForAPIStatus(ctx, t, h, id, "cancelled", 2*time.Second)
	if cmd := commandRow(t, h, id, cmdID); cmd.Status != "cancelled" || !strings.Contains(cmd.Error, "cancelled by the server") {
		t.Errorf("expected the command cancelled by the server, got %+v", cmd)
	}
}

// TestCancel_ShowsCancellingUntilTheAgentStops checks that a cancelled
// deployment shows as cancelling while its agent still runs a command, and
// that a command which finishes anyway is recorded as it finished.
func TestCancel_ShowsCancellingUntilTheAgentStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, release := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate, IgnoreCancel: true})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	cmdID := fa.WaitForCommands(ctx, t, 1)[0].ID
	if code, resp := cancelDeployment(ctx, t, h, id); code != http.StatusNoContent {
		t.Fatalf("cancel: expected 204, got %d: %s", code, resp)
	}
	waitForCancel(t, fa, cmdID)
	waitForAPIStatus(ctx, t, h, id, string(shared.DeploymentStatusCancelling), time.Second)

	if code, _ := cancelDeployment(ctx, t, h, id); code != http.StatusConflict {
		t.Errorf("expected a second cancel to conflict, got %d", code)
	}

	release()
	waitForAPIStatus(ctx, t, h, id, "cancelled", 2*time.Second)
	if cmd := commandRow(t, h, id, cmdID); cmd.Status != "succeeded" {
		t.Errorf("expected the command recorded as it finished, got %+v", cmd)
	}
}

// TestCancel_ReachesTheAgentOnceItReconnects checks that a deployment
// cancelled while its agent was disconnected shows as cancelling, and that
// the agent is told to stop the command when it comes back running it.
func TestCancel_ReachesTheAgentOnceItReconnects(t *testing.T) {
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
	waitForAPIStatus(ctx, t, h, id, string(shared.DeploymentStatusCancelling), time.Second)

	agent := h.ConnectAgent(ctx, t, scenario.AgentToken, wire.HelloPayload{Running: &cmdID})
	msg := agent.Receive(ctx)
	var stop wire.CancelPayload
	if msg.Type != wire.MessageCancel || json.Unmarshal(msg.Data, &stop) != nil || stop.CommandID != cmdID {
		t.Fatalf("expected a cancel for %s, got %s %s", cmdID, msg.Type, msg.Data)
	}
	agent.Result(ctx, &wire.ResultPayload{CommandID: cmdID, Error: "cancelled by the server"})
	if msg := agent.Ready(ctx); msg.Type != wire.MessageIdle {
		t.Fatalf("expected idle, got %s", msg.Type)
	}
	waitForAPIStatus(ctx, t, h, id, "cancelled", time.Second)
	if cmd := commandRow(t, h, id, cmdID); cmd.Status != "cancelled" || cmd.Error != "cancelled by the server" {
		t.Errorf("expected the command cancelled by the server, got %+v", cmd)
	}
}

// TestCancel_CancelsQueuedCommandsAtOnce checks that commands no agent has
// picked up yet are cancelled by the cancel request itself.
func TestCancel_CancelsQueuedCommandsAtOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	req := SubmitRequest(scenario, ServiceConfigWithDeps("test-service", map[string]string{"db1": "postgres", "db2": "postgres"}), "v1.0.0")
	req.Dependencies = map[string]shared.DependencyInput{"db1": {Managed: true}, "db2": {Managed: true}}
	id := submitDeployment(ctx, t, h, req)
	running := fa.WaitForCommands(ctx, t, 1)[0].ID

	if code, resp := cancelDeployment(ctx, t, h, id); code != http.StatusNoContent {
		t.Fatalf("cancel: expected 204, got %d: %s", code, resp)
	}
	for _, cmd := range commandsOf(t, h.DB, id) {
		if cmd.ID != running && cmd.Status != "cancelled" {
			t.Errorf("expected the queued command cancelled by the request, got %+v", cmd)
		}
	}
	waitForCancel(t, fa, running)
	waitForAPIStatus(ctx, t, h, id, "cancelled", 2*time.Second)
}

// TestCancel_ThroughTheCLIClient checks the CLI client's cancel: it succeeds
// on a deployment in progress, and reports the server's refusal on one that
// has ended.
func TestCancel_ThroughTheCLIClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	id := submitDeployment(ctx, t, h, SubmitRequest(scenario, MinimalServiceConfig("test-service"), "v1.0.0"))
	fa.WaitForCommands(ctx, t, 1)

	c := h.Client()
	if err := c.CancelDeployment(ctx, id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitForAPIStatus(ctx, t, h, id, "cancelled", 2*time.Second)
	if err := c.CancelDeployment(ctx, id); err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("cancelling again: expected a 409, got %v", err)
	}
}

// TestStateMachine_FailedDependencyCancelsItsQueuedSibling checks that when
// one dependency fails, another that the agent has not started on is
// cancelled and never handed out.
func TestStateMachine_FailedDependencyCancelsItsQueuedSibling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, FailingBehavior("tofu apply: exit status 1"))

	req := SubmitRequest(scenario, ServiceConfigWithDeps("test-service", map[string]string{"db1": "postgres", "db2": "postgres"}), "v1.0.0")
	req.Dependencies = map[string]shared.DependencyInput{"db1": {Managed: true}, "db2": {Managed: true}}
	id := submitDeployment(ctx, t, h, req)

	body := waitForTerminal(ctx, t, h, id, 5*time.Second)
	if got := field(body, "status"); got != "failed" || !strings.Contains(field(body, "error"), "failed to provision: tofu apply: exit status 1") {
		t.Fatalf("expected failed with the dependency's error, got %q %q\n%s", got, field(body, "error"), describeDeployment(h.DB, id))
	}
	failed := fa.WaitForCommands(ctx, t, 1)[0].ID
	time.Sleep(5 * fakeAgentPollInterval)
	if got := fa.ReceivedCommands(); len(got) != 1 {
		t.Fatalf("expected the agent to get only the failed tofu.apply, got %d commands", len(got))
	}
	for _, cmd := range commandsOf(t, h.DB, id) {
		if cmd.ID != failed && cmd.Status != "cancelled" {
			t.Errorf("expected the sibling cancelled, got %+v", cmd)
		}
	}
}
