//go:build integration

package integration

// Known bugs in the deployment state machine. Each test asserts the correct
// behaviour, so it fails until its fix lands; the doc comment names the audit
// ID and the PR that fixes it. Do not skip these or weaken them to match
// today's behaviour.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/agent/wire"
	shared "github.com/pondplatform/pond/shared/server/api"
)

var managedPostgres = map[string]shared.DependencyInput{
	"postgres": {Managed: true, Values: map[string]any{"instances": 1}},
}

// TestStateMachine_TwoManagedDepsRunOnce checks that each managed dependency
// is provisioned once and helm then runs once.
// H2; fixed by PR 3.
func TestStateMachine_TwoManagedDepsRunOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	h.StartFakeAgent(ctx, t, scenario.AgentToken, TofuOutputBehavior(PostgresOutput))

	req := SubmitRequest(scenario, ServiceConfigWithDeps("test-service", map[string]string{
		"db1": "postgres",
		"db2": "postgres",
	}), "v1.0.0")
	req.Dependencies = map[string]shared.DependencyInput{
		"db1": {Managed: true, Values: map[string]any{"instances": 1}},
		"db2": {Managed: true, Values: map[string]any{"instances": 1}},
	}
	id := submitDeployment(ctx, t, h, req)

	body := waitForTerminal(ctx, t, h, id, 5*time.Second)
	if got := field(body, "status"); got != "succeeded" {
		t.Fatalf("expected status succeeded, got %q (error %q)\n%s", got, field(body, "error"), describeDeployment(h.DB, id))
	}

	// A rescheduling loop keeps adding commands after the deployment looks
	// done, so count again a second later.
	assertCommandCounts(t, h, id, 2, 1)
	time.Sleep(time.Second)
	assertCommandCounts(t, h, id, 2, 1)
}

// TestStateMachine_AllUnmanagedOnSubmitSucceeds checks that a deployment whose
// dependencies are all unmanaged, configured on submit, goes straight to helm:
// there is nothing to provision.
// H3; fixed by PR 3.
func TestStateMachine_AllUnmanagedOnSubmitSucceeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)

	req := SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.0")
	req.Dependencies = map[string]shared.DependencyInput{
		"postgres": {Managed: false, Values: PostgresUserConfig},
	}
	id := submitDeployment(ctx, t, h, req)

	body := waitForTerminal(ctx, t, h, id, 5*time.Second)
	if got := field(body, "status"); got != "succeeded" {
		t.Fatalf("expected status succeeded, got %q (error %q)\n%s", got, field(body, "error"), describeDeployment(h.DB, id))
	}
	assertCommandCounts(t, h, id, 0, 1)
}

// TestStateMachine_RenderFailureAfterDepsFails checks that a config template
// that cannot be rendered from the dependency outputs fails the deployment,
// naming the missing reference, instead of leaving it pending.
// H1; fixed by PR 3.
func TestStateMachine_RenderFailureAfterDepsFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	h.StartFakeAgent(ctx, t, scenario.AgentToken, TofuOutputBehavior(PostgresOutput))

	cfg := WithConfigValues(ServiceConfigWithDep("test-service"), map[string]any{
		"db_user": "{{postgres.username}}",
	})
	req := SubmitRequest(scenario, cfg, "v1.0.0")
	req.Dependencies = managedPostgres
	id := submitDeployment(ctx, t, h, req)

	body := waitForTerminal(ctx, t, h, id, 5*time.Second)
	if got := field(body, "status"); got != "failed" {
		t.Fatalf("expected status failed, got %q\n%s", got, describeDeployment(h.DB, id))
	}
	if errMsg := field(body, "error"); !strings.Contains(errMsg, "postgres.username") {
		t.Errorf("expected the error to name postgres.username, got %q", errMsg)
	}
	if n := countCommands(t, h.DB, id, wire.CommandHelmUpgrade); n != 0 {
		t.Errorf("expected no helm.upgrade command, got %d\n%s", n, describeDeployment(h.DB, id))
	}
}

// TestStateMachine_NonObjectTofuOutputFails checks that a tofu.apply output
// that is not a JSON object fails the deployment, naming the dependency,
// instead of leaving it pending.
// H1; fixed by PR 3.
func TestStateMachine_NonObjectTofuOutputFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	h.StartFakeAgent(ctx, t, scenario.AgentToken, PerCommandBehavior(map[wire.CommandType]func(*wire.CommandPayload) *wire.ResultPayload{
		wire.CommandTofuApply: func(cmd *wire.CommandPayload) *wire.ResultPayload {
			return &wire.ResultPayload{CommandID: cmd.ID, Success: true, Output: json.RawMessage(`[1]`)}
		},
	}))

	req := SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.0")
	req.Dependencies = managedPostgres
	id := submitDeployment(ctx, t, h, req)

	body := waitForTerminal(ctx, t, h, id, 5*time.Second)
	if got := field(body, "status"); got != "failed" {
		t.Fatalf("expected status failed, got %q\n%s", got, describeDeployment(h.DB, id))
	}
	if errMsg := field(body, "error"); !strings.Contains(errMsg, "postgres") {
		t.Errorf("expected the error to name the postgres dependency, got %q", errMsg)
	}
	if n := countCommands(t, h.DB, id, wire.CommandHelmUpgrade); n != 0 {
		t.Errorf("expected no helm.upgrade command, got %d\n%s", n, describeDeployment(h.DB, id))
	}
}

// TestStateMachine_LateHelmResultAfterCancelIgnored checks that a helm result
// arriving after a cancel does not turn the cancelled deployment into
// succeeded.
// H13; fixed by PR 3.
func TestStateMachine_LateHelmResultAfterCancelIgnored(t *testing.T) {
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
	release()

	// Cancelling until the late result arrives, then cancelled for good.
	waitForAPIStatus(ctx, t, h, id, "cancelled", 2*time.Second)
	assertStatusHolds(ctx, t, h, id, "cancelled", time.Second)
	waitForCommandStatus(t, h, id, cmdID, "succeeded", time.Second)
}

// TestStateMachine_LateTofuResultAfterCancelIgnored checks that a tofu result
// arriving after a cancel neither revives the deployment nor starts helm.
// H13; fixed by PR 3.
func TestStateMachine_LateTofuResultAfterCancelIgnored(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	gate, release := NewGate(t)
	behavior := TofuOutputBehavior(PostgresOutput)
	behavior.Gate = gate
	behavior.IgnoreCancel = true
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, behavior)

	req := SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.0")
	req.Dependencies = managedPostgres
	id := submitDeployment(ctx, t, h, req)
	fa.WaitForCommands(ctx, t, 1)

	if code, resp := cancelDeployment(ctx, t, h, id); code != http.StatusNoContent {
		t.Fatalf("cancel: expected 204, got %d: %s", code, resp)
	}
	release()

	// Cancelling until the late result arrives, then cancelled for good.
	waitForAPIStatus(ctx, t, h, id, "cancelled", 2*time.Second)
	assertStatusHolds(ctx, t, h, id, "cancelled", time.Second)
	if n := countCommands(t, h.DB, id, wire.CommandHelmUpgrade); n != 0 {
		t.Errorf("expected no helm.upgrade command, got %d\n%s", n, describeDeployment(h.DB, id))
	}
}

// TestCancel_CancelsDispatchedCommand checks that a cancel also cancels the
// command the agent is running, not only queued ones. Telling the agent to
// abort needs a protocol message that does not exist yet; this covers the
// server's side.
// H13; fixed by PR 3.
func TestCancel_CancelsDispatchedCommand(t *testing.T) {
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
	waitForCommandStatus(t, h, id, cmdID, "cancelled", 2*time.Second)
}

// assertCommandCounts checks how many tofu.apply and helm.upgrade commands
// the deployment has.
func assertCommandCounts(t *testing.T, h *TestHarness, id uuid.UUID, tofu, helm int) {
	t.Helper()
	gotTofu := countCommands(t, h.DB, id, wire.CommandTofuApply)
	gotHelm := countCommands(t, h.DB, id, wire.CommandHelmUpgrade)
	if gotTofu != tofu || gotHelm != helm {
		t.Errorf("expected %d tofu.apply and %d helm.upgrade commands, got %d and %d\n%s",
			tofu, helm, gotTofu, gotHelm, describeDeployment(h.DB, id))
	}
}

// waitForCommandStatus fails the test unless the command reaches status
// within timeout.
func waitForCommandStatus(t *testing.T, h *TestHarness, deploymentID, cmdID uuid.UUID, status string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var got string
		if err := h.DB.QueryRow(`SELECT status FROM commands WHERE id = $1`, cmdID).Scan(&got); err != nil {
			t.Fatalf("query command %s: %v", cmdID, err)
		}
		if got == status {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected command %s to be %q within %s, still %q\n%s",
				cmdID, status, timeout, got, describeDeployment(h.DB, deploymentID))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestDeployment_SecondSubmitReusesDependencyConfig checks that a submit
// without input reuses the configuration of the service's previous deployment
// instead of waiting for input again.
func TestDeployment_SecondSubmitReusesDependencyConfig(t *testing.T) {
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

	secondID := submitDeployment(ctx, t, h, SubmitRequest(scenario, ServiceConfigWithDep("test-service"), "v1.0.1"))
	if got := field(waitForTerminal(ctx, t, h, secondID, 5*time.Second), "status"); got != "succeeded" {
		t.Fatalf("second deployment: expected succeeded, got %q\n%s", got, describeDeployment(h.DB, secondID))
	}
	assertCommandCounts(t, h, secondID, 1, 1)
}
