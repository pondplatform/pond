package commands_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	"github.com/pondplatform/pond/cli/internal/commands"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
)

// fakeClient serves a fixed project with two environments and one deployment.
type fakeClient struct {
	services    []api.Service
	envs        []api.Environment
	deployments map[uuid.UUID][]api.DeploymentListItem // keyed by environment ID
	detail      *api.Deployment
	submitted   []api.SubmitRequest
}

func (f *fakeClient) SubmitDeployment(_ context.Context, req api.SubmitRequest) (*api.Deployment, error) {
	f.submitted = append(f.submitted, req)
	return &api.Deployment{ID: uuid.MustParse(testDeployID), Status: api.DeploymentStatusPending}, nil
}

func (f *fakeClient) GetDeployment(_ context.Context, id uuid.UUID) (*api.Deployment, error) {
	if f.detail == nil || f.detail.ID != id {
		return nil, fmt.Errorf("deployment %s not found", id)
	}
	return f.detail, nil
}

func (f *fakeClient) GetCommandLogs(context.Context, uuid.UUID) ([]api.CommandLog, error) {
	return nil, nil
}

func (f *fakeClient) ListServices(context.Context, uuid.UUID) ([]api.Service, error) {
	return f.services, nil
}

func (f *fakeClient) ListEnvironments(context.Context, uuid.UUID) ([]api.Environment, error) {
	return f.envs, nil
}

// ListDeployments returns deployments newest first, across all environments
// when envID is nil.
func (f *fakeClient) ListDeployments(_ context.Context, _ uuid.UUID, envID *uuid.UUID, limit int) ([]api.DeploymentListItem, error) {
	var items []api.DeploymentListItem
	if envID != nil {
		items = append(items, f.deployments[*envID]...)
	} else {
		for _, envItems := range f.deployments {
			items = append(items, envItems...)
		}
	}

	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

const (
	testProjectID = "11111111-1111-1111-1111-111111111111"
	testEnvProdID = "22222222-2222-2222-2222-222222222222"
	testEnvDevID  = "33333333-3333-3333-3333-333333333333"
	testServiceID = "44444444-4444-4444-4444-444444444444"
	testDeployID  = "55555555-5555-5555-5555-555555555555"
)

func newFakeClient() *fakeClient {
	created := time.Date(2026, 9, 12, 10, 30, 0, 0, time.UTC)
	deployment := api.DeploymentListItem{
		ID:            uuid.MustParse(testDeployID),
		ServiceID:     uuid.MustParse(testServiceID),
		EnvironmentID: uuid.MustParse(testEnvProdID),
		ImageTag:      "v1.2.3",
		Status:        api.DeploymentStatusSucceeded,
		TriggeredBy:   "cli",
		CreatedAt:     created,
	}

	return &fakeClient{
		services: []api.Service{
			{ID: uuid.MustParse(testServiceID), ProjectID: uuid.MustParse(testProjectID), Name: "api"},
			{ID: uuid.New(), ProjectID: uuid.MustParse(testProjectID), Name: "worker"},
		},
		envs: []api.Environment{
			{ID: uuid.MustParse(testEnvProdID), Name: "prod"},
			{ID: uuid.MustParse(testEnvDevID), Name: "dev"},
		},
		deployments: map[uuid.UUID][]api.DeploymentListItem{
			uuid.MustParse(testEnvProdID): {deployment},
		},
		detail: &api.Deployment{
			ID:          uuid.MustParse(testDeployID),
			ServiceID:   uuid.MustParse(testServiceID),
			ImageTag:    "v1.2.3",
			Status:      api.DeploymentStatusSucceeded,
			TriggeredBy: "cli",
			CreatedAt:   created,
		},
	}
}

func runStatusCmd(t *testing.T, c client.ServerClient, env string, args ...string) (string, error) {
	t.Helper()
	ctx := context.WithValue(context.Background(), pondconfig.ClientKey{}, c)
	ctx = context.WithValue(ctx, pondconfig.ResolvedConfigKey{}, pondconfig.ResolvedConfig{
		Server:  "http://localhost:8080",
		Project: testProjectID,
		Env:     env,
	})

	cmd := commands.NewStatusCmd()
	cmd.SilenceUsage = true
	cmd.SetContext(ctx)
	return runCmd(t, cmd, args...)
}

func TestStatusServiceAllEnvironments(t *testing.T) {
	out, err := runStatusCmd(t, newFakeClient(), "", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Service api") {
		t.Errorf("expected service name in output, got:\n%s", out)
	}
	// Environments are listed alphabetically; dev has never been deployed to.
	devLine, prodLine := lineContaining(t, out, "dev"), lineContaining(t, out, "prod")
	if strings.Index(out, devLine) > strings.Index(out, prodLine) {
		t.Errorf("expected environments sorted by name, got:\n%s", out)
	}
	if !strings.Contains(prodLine, "succeeded") || !strings.Contains(prodLine, "v1.2.3") || !strings.Contains(prodLine, testDeployID) {
		t.Errorf("expected prod row to show the latest deployment, got: %q", prodLine)
	}
	if strings.Contains(devLine, "succeeded") {
		t.Errorf("expected dev row to have no deployment, got: %q", devLine)
	}
}

func TestStatusServiceSingleEnvironmentShowsDetail(t *testing.T) {
	out, err := runStatusCmd(t, newFakeClient(), "prod", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Service api (environment: prod)") {
		t.Errorf("expected service and environment header, got:\n%s", out)
	}
	if !strings.Contains(out, "Deployment "+testDeployID) {
		t.Errorf("expected deployment detail, got:\n%s", out)
	}
	if strings.Contains(out, "ENVIRONMENT") {
		t.Errorf("expected detail view rather than the per-environment table, got:\n%s", out)
	}
}

func TestStatusServiceSingleEnvironmentWithoutDeployment(t *testing.T) {
	out, err := runStatusCmd(t, newFakeClient(), "dev", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No deployments yet") {
		t.Errorf("expected a no-deployments notice, got:\n%s", out)
	}
}

func TestStatusUnknownEnvironment(t *testing.T) {
	_, err := runStatusCmd(t, newFakeClient(), "staging", "api")
	if err == nil {
		t.Fatal("expected an error for an unknown environment")
	}
	if !strings.Contains(err.Error(), "staging") || !strings.Contains(err.Error(), "prod") {
		t.Errorf("expected error to name the environment and the available ones, got: %v", err)
	}
}

func TestStatusUnknownServiceListsAvailable(t *testing.T) {
	_, err := runStatusCmd(t, newFakeClient(), "", "nope")
	if err == nil {
		t.Fatal("expected an error for an unknown service")
	}
	if !strings.Contains(err.Error(), "api, worker") {
		t.Errorf("expected error to list available services, got: %v", err)
	}
}

func TestStatusServiceNameFromPondYml(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pond.yml"), "version: 1\nname: api\nimage: ghcr.io/acme/api\n")
	t.Chdir(dir)

	out, err := runStatusCmd(t, newFakeClient(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Service api") {
		t.Errorf("expected the service name from pond.yml, got:\n%s", out)
	}
}

func TestStatusServiceNameFromExplicitConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.yml")
	writeFile(t, path, "version: 1\nname: api\n")

	out, err := runStatusCmd(t, newFakeClient(), "", "--pond-file", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Service api") {
		t.Errorf("expected the service name from %s, got:\n%s", path, out)
	}
}

func TestStatusPositionalArgumentBeatsPondYml(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pond.yml"), "version: 1\nname: api\n")
	t.Chdir(dir)

	out, err := runStatusCmd(t, newFakeClient(), "", "worker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Service worker") {
		t.Errorf("expected the service from the argument, got:\n%s", out)
	}
}

func TestStatusConfigWithoutServiceName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pond.yml"), "version: 1\nimage: ghcr.io/acme/api\n")
	t.Chdir(dir)

	_, err := runStatusCmd(t, newFakeClient(), "")
	if err == nil {
		t.Fatal("expected an error for a pond.yml without a name")
	}
	if !strings.Contains(err.Error(), "does not set a service name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStatusRequiresServiceOrDeploymentID(t *testing.T) {
	t.Chdir(t.TempDir()) // no pond.yml to fall back on

	_, err := runStatusCmd(t, newFakeClient(), "")
	if err == nil {
		t.Fatal("expected an error when no service is given")
	}
	if !strings.Contains(err.Error(), "service name is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStatusRejectsServiceAndDeploymentID(t *testing.T) {
	_, err := runStatusCmd(t, newFakeClient(), "", "api", "--deployment-id", testDeployID)
	if err == nil {
		t.Fatal("expected an error when both a service and --deployment-id are given")
	}
	if !strings.Contains(err.Error(), "not both") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStatusByDeploymentID(t *testing.T) {
	out, err := runStatusCmd(t, newFakeClient(), "", "--deployment-id", testDeployID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Deployment "+testDeployID) {
		t.Errorf("expected deployment detail, got:\n%s", out)
	}
}

// followClient serves a scripted sequence of deployment snapshots: each
// GetDeployment call advances to the next one, and the last one repeats.
type followClient struct {
	*fakeClient
	polls []followPoll
	n     int
}

type followPoll struct {
	deployment *api.Deployment
	logs       map[uuid.UUID][]string
}

func (f *followClient) GetDeployment(context.Context, uuid.UUID) (*api.Deployment, error) {
	if f.n < len(f.polls) {
		f.n++
	}
	return f.polls[f.n-1].deployment, nil
}

func (f *followClient) GetCommandLogs(_ context.Context, id uuid.UUID) ([]api.CommandLog, error) {
	var logs []api.CommandLog
	for _, line := range f.polls[f.n-1].logs[id] {
		logs = append(logs, api.CommandLog{Line: line})
	}
	return logs, nil
}

const testCommandID = "66666666-6666-6666-6666-666666666666"

func followSnapshot(status api.DeploymentStatus, cmdStatus api.CommandStatus, cmdErr string, lines ...string) followPoll {
	d := *newFakeClient().detail
	d.Status = status
	d.Commands = []api.CommandSummary{{ID: uuid.MustParse(testCommandID), Type: "deploy", Status: cmdStatus, Error: cmdErr}}
	return followPoll{deployment: &d, logs: map[uuid.UUID][]string{uuid.MustParse(testCommandID): lines}}
}

func TestStatusFollowStreamsLogsUntilDone(t *testing.T) {
	commands.SetFollowPollInterval(t, time.Millisecond)
	c := &followClient{fakeClient: newFakeClient(), polls: []followPoll{
		followSnapshot(api.DeploymentStatusRunning, api.CommandStatusQueued, ""),
		followSnapshot(api.DeploymentStatusRunning, api.CommandStatusDispatched, "", "pulling image"),
		followSnapshot(api.DeploymentStatusRunning, api.CommandStatusDispatched, "", "pulling image", "installing"),
		followSnapshot(api.DeploymentStatusSucceeded, api.CommandStatusSucceeded, "", "pulling image", "installing", "done"),
	}}

	out, err := runStatusCmd(t, c, "prod", "api", "--follow")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	cmdPrefix := "Command " + testCommandID + " (deploy): "
	want := []string{
		cmdPrefix + "queued",
		cmdPrefix + "dispatched",
		"  pulling image",
		"  installing",
		"  done",
		cmdPrefix + "succeeded",
		"Deployment succeeded",
	}
	pos := 0
	for _, w := range want {
		i := strings.Index(out[pos:], w)
		if i < 0 {
			t.Fatalf("expected %q after position %d, got:\n%s", w, pos, out)
		}
		pos += i + len(w)
	}
	if n := strings.Count(out, "pulling image"); n != 1 {
		t.Errorf("expected each log line once, got %q %d times:\n%s", "pulling image", n, out)
	}
}

func TestStatusFollowFailedDeploymentReturnsError(t *testing.T) {
	commands.SetFollowPollInterval(t, time.Millisecond)
	failed := followSnapshot(api.DeploymentStatusFailed, api.CommandStatusFailed, "helm exited 1", "boom")
	reason := "command deploy failed"
	failed.deployment.Error = &reason
	c := &followClient{fakeClient: newFakeClient(), polls: []followPoll{failed}}

	out, err := runStatusCmd(t, c, "", "--deployment-id", testDeployID, "--follow")
	if err == nil {
		t.Fatalf("expected an error for a failed deployment, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("expected the error to carry the failure reason, got: %v", err)
	}
	for _, w := range []string{"  boom", "Error: helm exited 1", "Deployment failed"} {
		if !strings.Contains(out, w) {
			t.Errorf("expected %q in output, got:\n%s", w, out)
		}
	}
}

func TestStatusFollowNeedsSingleDeployment(t *testing.T) {
	_, err := runStatusCmd(t, newFakeClient(), "", "api", "--follow")
	if err == nil {
		t.Fatal("expected an error when following without an environment")
	}
	if !strings.Contains(err.Error(), "--deployment-id") {
		t.Errorf("expected the error to say how to pick a deployment, got: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func lineContaining(t *testing.T, out, substr string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", substr, out)
	return ""
}
