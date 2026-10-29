//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/agent/wire"
	shared "github.com/pondplatform/pond/shared/server/api"
)

// deleteService POSTs a delete of the service and returns the status code
// and, on 202, the delete deployments.
func deleteService(ctx context.Context, t *testing.T, h *TestHarness, serviceID uuid.UUID, keepDeps bool) (int, []shared.Deployment, string) {
	t.Helper()
	code, body := postJSON(ctx, t, h, fmt.Sprintf("/api/v1/services/%s/delete", serviceID),
		shared.DeleteServiceRequest{KeepDependencies: keepDeps, TriggeredBy: "integration-test"})
	if code != http.StatusAccepted {
		return code, nil, body
	}
	var resp shared.DeleteServiceResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode delete response: %v\n%s", err, body)
	}
	return code, resp.Deployments, body
}

// mustDeleteService deletes the service and fails the test unless it gets
// want delete deployments.
func mustDeleteService(ctx context.Context, t *testing.T, h *TestHarness, serviceID uuid.UUID, keepDeps bool, want int) []shared.Deployment {
	t.Helper()
	code, deployments, body := deleteService(ctx, t, h, serviceID, keepDeps)
	if code != http.StatusAccepted {
		t.Fatalf("delete: expected 202, got %d: %s", code, body)
	}
	if len(deployments) != want {
		t.Fatalf("expected %d delete deployments, got %d: %s", want, len(deployments), body)
	}
	for _, d := range deployments {
		if d.Kind != shared.DeploymentKindDelete {
			t.Errorf("expected kind delete, got %+v", d)
		}
	}
	return deployments
}

// serviceNamed returns the project's service with the given name, failing the
// test if the listing doesn't have it: a deleted service is still listed.
func serviceNamed(ctx context.Context, t *testing.T, h *TestHarness, projectID uuid.UUID, name string) shared.Service {
	t.Helper()
	services, err := h.Client().ListServices(ctx, projectID)
	if err != nil {
		t.Fatalf("list services: %v", err)
	}
	for _, svc := range services {
		if svc.Name == name {
			return svc
		}
	}
	t.Fatalf("service %q not listed, got %+v", name, services)
	return shared.Service{}
}

func assertServiceStatus(ctx context.Context, t *testing.T, h *TestHarness, projectID uuid.UUID, name string, want shared.ServiceStatus) {
	t.Helper()
	if got := serviceNamed(ctx, t, h, projectID, name).Status; got != want {
		t.Errorf("expected service %q to be %s, got %q", name, want, got)
	}
}

// deployAndWait submits req and fails the test unless it succeeds.
func deployAndWait(ctx context.Context, t *testing.T, h *TestHarness, req shared.SubmitRequest) uuid.UUID {
	t.Helper()
	id := submitDeployment(ctx, t, h, req)
	if body := waitForTerminal(ctx, t, h, id, 10*time.Second); field(body, "status") != "succeeded" {
		t.Fatalf("expected the deployment to succeed, got %q (error %q)\n%s", field(body, "status"), field(body, "error"), describeDeployment(h.DB, id))
	}
	return id
}

func waitForSucceeded(ctx context.Context, t *testing.T, h *TestHarness, id uuid.UUID) {
	t.Helper()
	if body := waitForTerminal(ctx, t, h, id, 10*time.Second); field(body, "status") != "succeeded" {
		t.Fatalf("expected %s to succeed, got %q (error %q)\n%s", id, field(body, "status"), field(body, "error"), describeDeployment(h.DB, id))
	}
}

func received(fa *FakeAgent, cmdType wire.CommandType) []*wire.CommandPayload {
	var cmds []*wire.CommandPayload
	for _, cmd := range fa.ReceivedCommands() {
		if cmd.Type == cmdType {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func decodePayload[T any](t *testing.T, cmd *wire.CommandPayload) T {
	t.Helper()
	var payload T
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		t.Fatalf("decode %s payload: %v", cmd.Type, err)
	}
	return payload
}

// holdDeletes is a behavior that answers tofu.apply with postgres outputs and
// holds every service.delete until the returned release is called.
func holdDeletes(t *testing.T) (Behavior, func()) {
	gate, release := NewGate(t)
	outputs, _ := json.Marshal(PostgresOutput)
	return PerCommandBehavior(map[wire.CommandType]func(*wire.CommandPayload) *wire.ResultPayload{
		wire.CommandTofuApply: func(cmd *wire.CommandPayload) *wire.ResultPayload {
			return &wire.ResultPayload{CommandID: cmd.ID, Success: true, Output: outputs}
		},
		wire.CommandServiceDelete: func(cmd *wire.CommandPayload) *wire.ResultPayload {
			<-gate
			return &wire.ResultPayload{CommandID: cmd.ID, Success: true}
		},
	}), release
}

// secondEnvironment adds "test-env-2" on a cluster of its own to the scenario's
// project, and returns it with the cluster's agent token.
func secondEnvironment(ctx context.Context, t *testing.T, h *TestHarness, scenario ScenarioResult) (uuid.UUID, string) {
	t.Helper()
	sc := newScenarioClient(h.BaseURL, h.AdminToken)
	cluster, err := sc.createCluster(ctx, "test-cluster-2-"+uuid.New().String()[:8])
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	env, err := sc.createEnvironment(ctx, scenario.ProjectID, "test-env-2", "test-namespace-2", cluster.ID, "")
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	return env.ID, cluster.AgentToken
}

// TestServiceDelete_TearsDownWhatTheDeployCreated deploys a service with a
// managed postgres, deletes it, and checks the teardown names the release and
// the dependency exactly as the deploy created them, that the service shows
// as deleting, then deleted, and that a redeploy revives it.
func TestServiceDelete_TearsDownWhatTheDeployCreated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	behavior, release := holdDeletes(t)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, behavior)

	req := SubmitRequest(scenario, ServiceConfigWithDep("api"), "v1.0.0")
	req.Dependencies = managedPostgres
	deployed, err := h.Client().SubmitDeployment(ctx, req)
	if err != nil {
		t.Fatalf("submit deployment: %v", err)
	}
	if deployed.Kind != shared.DeploymentKindDeploy {
		t.Errorf("expected the submit to answer with kind deploy, got %q", deployed.Kind)
	}
	waitForSucceeded(ctx, t, h, deployed.ID)
	svc := serviceNamed(ctx, t, h, scenario.ProjectID, "api")
	if svc.Status != shared.ServiceStatusActive {
		t.Errorf("expected a deployed service to be active, got %q", svc.Status)
	}

	deletes := mustDeleteService(ctx, t, h, svc.ID, false, 1)
	if deletes[0].EnvironmentID != scenario.EnvID {
		t.Errorf("expected the delete in %s, got %+v", scenario.EnvID, deletes[0])
	}
	fa.WaitForCommands(ctx, t, 3)
	assertServiceStatus(ctx, t, h, scenario.ProjectID, "api", shared.ServiceStatusDeleting)

	teardown := decodePayload[wire.ServiceDeletePayload](t, received(fa, wire.CommandServiceDelete)[0])
	apply := decodePayload[wire.TofuApplyPayload](t, received(fa, wire.CommandTofuApply)[0])
	if teardown.Release == nil || *teardown.Release != (wire.HelmRelease{ReleaseName: "api", Namespace: "test-namespace"}) {
		t.Errorf("expected release api in test-namespace, got %+v", teardown.Release)
	}
	if len(teardown.Dependencies) != 1 {
		t.Fatalf("expected the postgres dependency, got %+v", teardown.Dependencies)
	}
	dep := teardown.Dependencies[0]
	applyVars, _ := json.Marshal(apply.Vars)
	destroyVars, _ := json.Marshal(dep.Vars)
	if dep.Name != "postgres" || dep.WorkDir != apply.WorkDir || dep.StatePath != apply.StatePath || string(applyVars) != string(destroyVars) {
		t.Errorf("expected the apply's payload, got %+v\napply %+v", dep, apply)
	}

	release()
	waitForSucceeded(ctx, t, h, deletes[0].ID)
	assertServiceStatus(ctx, t, h, scenario.ProjectID, "api", shared.ServiceStatusDeleted)
	mustDeleteService(ctx, t, h, svc.ID, false, 0)

	deployAndWait(ctx, t, h, req)
	if again := serviceNamed(ctx, t, h, scenario.ProjectID, "api"); again.ID != svc.ID || again.Status != shared.ServiceStatusActive {
		t.Errorf("expected the redeploy to revive service %s as active, got %+v", svc.ID, again)
	}
}

// TestServiceDelete_KeepDependencies checks that keepDependencies only
// uninstalls the release.
func TestServiceDelete_KeepDependencies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, TofuOutputBehavior(PostgresOutput))

	req := SubmitRequest(scenario, ServiceConfigWithDep("api"), "v1.0.0")
	req.Dependencies = managedPostgres
	deployAndWait(ctx, t, h, req)
	svc := serviceNamed(ctx, t, h, scenario.ProjectID, "api")

	deletes := mustDeleteService(ctx, t, h, svc.ID, true, 1)
	waitForSucceeded(ctx, t, h, deletes[0].ID)
	teardown := decodePayload[wire.ServiceDeletePayload](t, received(fa, wire.CommandServiceDelete)[0])
	if teardown.Release == nil || len(teardown.Dependencies) != 0 {
		t.Errorf("expected only the release, got %+v", teardown)
	}
}

// TestServiceDelete_EveryEnvironment deletes a service deployed to two
// environments on two clusters. It stays deleting until the second one has
// finished too.
func TestServiceDelete_EveryEnvironment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	env2, token2 := secondEnvironment(ctx, t, h, scenario)
	h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)
	held, release := holdDeletes(t)
	fa2 := h.StartFakeAgent(ctx, t, token2, held)

	req := SubmitRequest(scenario, MinimalServiceConfig("api"), "v1.0.0")
	deployAndWait(ctx, t, h, req)
	req.EnvironmentName = "test-env-2"
	deployAndWait(ctx, t, h, req)
	svc := serviceNamed(ctx, t, h, scenario.ProjectID, "api")

	deletes := mustDeleteService(ctx, t, h, svc.ID, false, 2)
	if deletes[0].EnvironmentID != scenario.EnvID || deletes[1].EnvironmentID != env2 {
		t.Fatalf("expected deletes in test-env, then test-env-2, got %+v", deletes)
	}
	waitForSucceeded(ctx, t, h, deletes[0].ID)
	fa2.WaitForCommands(ctx, t, 2)
	assertServiceStatus(ctx, t, h, scenario.ProjectID, "api", shared.ServiceStatusDeleting)

	release()
	waitForSucceeded(ctx, t, h, deletes[1].ID)
	assertServiceStatus(ctx, t, h, scenario.ProjectID, "api", shared.ServiceStatusDeleted)
}

// TestServiceDelete_RetriesOnlyWhatFailed checks that a service whose delete
// failed in one environment stays active, and that deleting it again only
// tears that environment down.
func TestServiceDelete_RetriesOnlyWhatFailed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	env2, token2 := secondEnvironment(ctx, t, h, scenario)
	h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)
	var failed atomic.Bool
	h.StartFakeAgent(ctx, t, token2, PerCommandBehavior(map[wire.CommandType]func(*wire.CommandPayload) *wire.ResultPayload{
		wire.CommandServiceDelete: func(cmd *wire.CommandPayload) *wire.ResultPayload {
			if failed.CompareAndSwap(false, true) {
				return &wire.ResultPayload{CommandID: cmd.ID, Error: "helm uninstall timed out"}
			}
			return &wire.ResultPayload{CommandID: cmd.ID, Success: true}
		},
	}))

	req := SubmitRequest(scenario, MinimalServiceConfig("api"), "v1.0.0")
	deployAndWait(ctx, t, h, req)
	req.EnvironmentName = "test-env-2"
	deployAndWait(ctx, t, h, req)
	svc := serviceNamed(ctx, t, h, scenario.ProjectID, "api")

	deletes := mustDeleteService(ctx, t, h, svc.ID, false, 2)
	waitForSucceeded(ctx, t, h, deletes[0].ID)
	body := waitForTerminal(ctx, t, h, deletes[1].ID, 10*time.Second)
	if field(body, "status") != "failed" || field(body, "error") != "teardown failed: helm uninstall timed out" {
		t.Fatalf("expected the second delete to fail with the agent's error, got %q (%q)", field(body, "status"), field(body, "error"))
	}
	assertServiceStatus(ctx, t, h, scenario.ProjectID, "api", shared.ServiceStatusActive)

	retry := mustDeleteService(ctx, t, h, svc.ID, false, 1)
	if retry[0].EnvironmentID != env2 {
		t.Errorf("expected the retry in test-env-2 only, got %+v", retry[0])
	}
	waitForSucceeded(ctx, t, h, retry[0].ID)
	assertServiceStatus(ctx, t, h, scenario.ProjectID, "api", shared.ServiceStatusDeleted)
}

// TestServiceDelete_RefusedWhileWorkIsUnfinished checks that a delete waits
// for no deployment, and a deploy for no delete: each is refused with 409.
func TestServiceDelete_RefusedWhileWorkIsUnfinished(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	rollout, releaseRollout := NewGate(t)
	teardown, releaseTeardown := NewGate(t)
	held := func(gate <-chan struct{}) func(*wire.CommandPayload) *wire.ResultPayload {
		return func(cmd *wire.CommandPayload) *wire.ResultPayload {
			<-gate
			return &wire.ResultPayload{CommandID: cmd.ID, Success: true}
		}
	}
	h.StartFakeAgent(ctx, t, scenario.AgentToken, PerCommandBehavior(map[wire.CommandType]func(*wire.CommandPayload) *wire.ResultPayload{
		wire.CommandHelmUpgrade:   held(rollout),
		wire.CommandServiceDelete: held(teardown),
	}))

	req := SubmitRequest(scenario, MinimalServiceConfig("api"), "v1.0.0")
	running := submitDeployment(ctx, t, h, req)
	svc := serviceNamed(ctx, t, h, scenario.ProjectID, "api")
	code, _, body := deleteService(ctx, t, h, svc.ID, false)
	if code != http.StatusConflict || !strings.Contains(body, "deployment "+running.String()) || !strings.Contains(body, "wait for it or cancel it") {
		t.Fatalf("expected 409 naming the running deployment, got %d: %s", code, body)
	}
	releaseRollout()
	waitForSucceeded(ctx, t, h, running)

	deletes := mustDeleteService(ctx, t, h, svc.ID, false, 1)
	_, err := h.Client().SubmitDeployment(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), `service \"api\" is being deleted from environment \"test-env\"`) {
		t.Fatalf("expected the deploy to be refused while the delete runs, got %v", err)
	}
	if code, _, body := deleteService(ctx, t, h, svc.ID, false); code != http.StatusConflict || !strings.Contains(body, "delete "+deletes[0].ID.String()) {
		t.Errorf("expected a second delete to be refused, got %d: %s", code, body)
	}
	releaseTeardown()
	waitForSucceeded(ctx, t, h, deletes[0].ID)
}

// TestServiceDelete_RefusedWhileAnotherServiceDependsOnIt checks that a
// provider can't be deleted while a consumer imports it, and can once the
// consumer is gone.
func TestServiceDelete_RefusedWhileAnotherServiceDependsOnIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenarioWithBaseHost(ctx, t, h, "apps.test")
	patchEnvironment(ctx, t, h, scenario.EnvID, map[string]any{"gatewayPort": 8000, "gatewayTls": false})
	h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)

	deployAndWait(ctx, t, h, SubmitFileRequest(t, scenario, "service-dependency/1-provider.yml"))
	deployAndWait(ctx, t, h, SubmitFileRequest(t, scenario, "service-dependency/2-consumer.yml"))
	provider := serviceNamed(ctx, t, h, scenario.ProjectID, "dep-provider")
	consumer := serviceNamed(ctx, t, h, scenario.ProjectID, "dep-consumer")

	code, _, body := deleteService(ctx, t, h, provider.ID, false)
	if code != http.StatusConflict || !strings.Contains(body, `service \"dep-provider\" is a dependency of dep-consumer in test-env`) {
		t.Fatalf("expected 409 naming the consumer, got %d: %s", code, body)
	}
	assertServiceStatus(ctx, t, h, scenario.ProjectID, "dep-provider", shared.ServiceStatusActive)

	waitForSucceeded(ctx, t, h, mustDeleteService(ctx, t, h, consumer.ID, false, 1)[0].ID)
	waitForSucceeded(ctx, t, h, mustDeleteService(ctx, t, h, provider.ID, false, 1)[0].ID)

	// The consumer can't come back while its provider is gone.
	_, err := h.Client().SubmitDeployment(ctx, SubmitFileRequest(t, scenario, "service-dependency/2-consumer.yml"))
	if err == nil || !strings.Contains(err.Error(), `service \"dep-provider\" was deleted from environment \"test-env\"`) {
		t.Errorf("expected the consumer's deploy to be refused, got %v", err)
	}
}

// TestServiceDelete_NothingToTearDown deletes a service that was never rolled
// out, keeping its dependency: the delete succeeds without a command.
func TestServiceDelete_NothingToTearDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, FailingBehavior("tofu apply failed"))

	req := SubmitRequest(scenario, ServiceConfigWithDep("api"), "v1.0.0")
	req.Dependencies = managedPostgres
	failed := submitDeployment(ctx, t, h, req)
	if body := waitForTerminal(ctx, t, h, failed, 10*time.Second); field(body, "status") != "failed" {
		t.Fatalf("expected the deploy to fail, got %q", field(body, "status"))
	}
	svc := serviceNamed(ctx, t, h, scenario.ProjectID, "api")

	deletes := mustDeleteService(ctx, t, h, svc.ID, true, 1)
	if deletes[0].Status != shared.DeploymentStatusSucceeded {
		t.Errorf("expected the delete to succeed at once, got %+v", deletes[0])
	}
	if n := countCommands(t, h.DB, deletes[0].ID, wire.CommandServiceDelete); n != 0 {
		t.Errorf("expected no command, got %d", n)
	}
	if got := received(fa, wire.CommandServiceDelete); len(got) != 0 {
		t.Errorf("expected nothing dispatched, got %+v", got)
	}
	assertServiceStatus(ctx, t, h, scenario.ProjectID, "api", shared.ServiceStatusDeleted)
}
