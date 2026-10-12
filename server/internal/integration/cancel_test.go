//go:build integration

package integration

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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

// TestStateMachine_FailedDependencyStopsItsRunningSibling checks that when one
// dependency fails, the agent provisioning another is told to stop.
func TestStateMachine_FailedDependencyStopsItsRunningSibling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, _ := NewGate(t)
	slow := h.StartFakeAgent(ctx, t, scenario.AgentToken, Behavior{Gate: gate})

	req := SubmitRequest(scenario, ServiceConfigWithDeps("test-service", map[string]string{"db1": "postgres", "db2": "postgres"}), "v1.0.0")
	req.Dependencies = map[string]shared.DependencyInput{"db1": {Managed: true}, "db2": {Managed: true}}
	id := submitDeployment(ctx, t, h, req)
	sibling := slow.WaitForCommands(ctx, t, 1)[0].ID

	h.StartFakeAgent(ctx, t, scenario.AgentToken, FailingBehavior("tofu apply: exit status 1"))

	waitForCancel(t, slow, sibling)
	body := waitForTerminal(ctx, t, h, id, 5*time.Second)
	if got := field(body, "status"); got != "failed" || !strings.Contains(field(body, "error"), "failed to provision: tofu apply: exit status 1") {
		t.Fatalf("expected failed with the dependency's error, got %q %q\n%s", got, field(body, "error"), describeDeployment(h.DB, id))
	}
	waitForCommandStatus(t, h, id, sibling, "cancelled", 2*time.Second)
}
