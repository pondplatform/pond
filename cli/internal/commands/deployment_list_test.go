package commands_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	"github.com/pondplatform/pond/cli/internal/commands"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
)

const testOlderDeployID = "66666666-6666-6666-6666-666666666666"

// newFakeClientWithHistory adds an older prod deployment and one in dev, so the
// listing spans environments and has an order to assert on.
func newFakeClientWithHistory() *fakeClient {
	f := newFakeClient()
	completed := time.Date(2026, 9, 10, 8, 15, 0, 0, time.UTC)

	f.deployments[uuid.MustParse(testEnvProdID)] = append(f.deployments[uuid.MustParse(testEnvProdID)],
		api.DeploymentListItem{
			ID:            uuid.MustParse(testOlderDeployID),
			ServiceID:     uuid.MustParse(testServiceID),
			EnvironmentID: uuid.MustParse(testEnvProdID),
			ImageTag:      "v1.0.0",
			Status:        api.DeploymentStatusFailed,
			TriggeredBy:   "ci",
			CreatedAt:     time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC),
			CompletedAt:   &completed,
		})

	f.deployments[uuid.MustParse(testEnvDevID)] = []api.DeploymentListItem{{
		ID:            uuid.New(),
		ServiceID:     uuid.MustParse(testServiceID),
		EnvironmentID: uuid.MustParse(testEnvDevID),
		ImageTag:      "v1.3.0-rc1",
		Status:        api.DeploymentStatusRunning,
		TriggeredBy:   "cli",
		CreatedAt:     time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC),
	}}

	return f
}

func runListCmd(t *testing.T, c client.ServerClient, env string, args ...string) (string, error) {
	t.Helper()
	ctx := context.WithValue(context.Background(), pondconfig.ClientKey{}, c)
	ctx = context.WithValue(ctx, pondconfig.ResolvedConfigKey{}, pondconfig.ResolvedConfig{
		Server:  "http://localhost:8080",
		Project: testProjectID,
		Env:     env,
	})

	cmd := commands.NewDeploymentListCmd()
	cmd.SilenceUsage = true
	cmd.SetContext(ctx)
	return runCmd(t, cmd, args...)
}

func TestDeploymentListAllEnvironments(t *testing.T) {
	out, err := runListCmd(t, newFakeClientWithHistory(), "", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Service api") {
		t.Errorf("expected the service name in output, got:\n%s", out)
	}
	if !strings.Contains(out, "ENVIRONMENT") {
		t.Errorf("expected an ENVIRONMENT column when listing every environment, got:\n%s", out)
	}

	// Deployments from every environment appear, newest first.
	newest, oldest := lineContaining(t, out, "v1.3.0-rc1"), lineContaining(t, out, "v1.0.0")
	if !strings.Contains(newest, "dev") || !strings.Contains(oldest, "prod") {
		t.Errorf("expected rows to name their environment, got:\n%s", out)
	}
	if strings.Index(out, newest) > strings.Index(out, lineContaining(t, out, "v1.2.3")) {
		t.Errorf("expected deployments sorted newest first, got:\n%s", out)
	}
	if strings.Index(out, lineContaining(t, out, "v1.2.3")) > strings.Index(out, oldest) {
		t.Errorf("expected deployments sorted newest first, got:\n%s", out)
	}
}

func TestDeploymentListShowsCompletionAndPlaceholder(t *testing.T) {
	out, err := runListCmd(t, newFakeClientWithHistory(), "", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(lineContaining(t, out, "v1.0.0"), "2026-09-10 08:15:00") {
		t.Errorf("expected the completion time of a finished deployment, got:\n%s", out)
	}
	// The running dev deployment has no completion time, so the last column of
	// its row is the placeholder rather than a timestamp.
	running := strings.Fields(lineContaining(t, out, "v1.3.0-rc1"))
	if last := running[len(running)-1]; last != "-" {
		t.Errorf("expected a placeholder in the completed column, got %q in:\n%s", last, out)
	}
}

func TestDeploymentListSingleEnvironmentOmitsEnvColumn(t *testing.T) {
	out, err := runListCmd(t, newFakeClientWithHistory(), "prod", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Service api (environment: prod)") {
		t.Errorf("expected the environment in the header, got:\n%s", out)
	}
	if strings.Contains(out, "ENVIRONMENT") {
		t.Errorf("expected no ENVIRONMENT column when scoped to one environment, got:\n%s", out)
	}
	if strings.Contains(out, "v1.3.0-rc1") {
		t.Errorf("expected deployments from other environments to be excluded, got:\n%s", out)
	}
	if !strings.Contains(out, "v1.2.3") || !strings.Contains(out, "v1.0.0") {
		t.Errorf("expected both prod deployments, got:\n%s", out)
	}
}

func TestDeploymentListLimit(t *testing.T) {
	out, err := runListCmd(t, newFakeClientWithHistory(), "", "api", "--limit", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "v1.3.0-rc1") {
		t.Errorf("expected the newest deployment, got:\n%s", out)
	}
	if strings.Contains(out, "v1.2.3") || strings.Contains(out, "v1.0.0") {
		t.Errorf("expected --limit to cap the number of rows, got:\n%s", out)
	}
}

func TestDeploymentListRejectsNonPositiveLimit(t *testing.T) {
	_, err := runListCmd(t, newFakeClientWithHistory(), "", "api", "--limit", "0")
	if err == nil {
		t.Fatal("expected an error for a non-positive limit")
	}
	if !strings.Contains(err.Error(), "--limit") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDeploymentListWithoutDeployments(t *testing.T) {
	out, err := runListCmd(t, newFakeClient(), "dev", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No deployments yet") {
		t.Errorf("expected a no-deployments notice, got:\n%s", out)
	}
}

func TestDeploymentListUnknownEnvironment(t *testing.T) {
	_, err := runListCmd(t, newFakeClient(), "staging", "api")
	if err == nil {
		t.Fatal("expected an error for an unknown environment")
	}
	if !strings.Contains(err.Error(), "staging") || !strings.Contains(err.Error(), "prod") {
		t.Errorf("expected the error to name the environment and the available ones, got: %v", err)
	}
}

func TestDeploymentListUnknownServiceListsAvailable(t *testing.T) {
	_, err := runListCmd(t, newFakeClient(), "", "nope")
	if err == nil {
		t.Fatal("expected an error for an unknown service")
	}
	if !strings.Contains(err.Error(), "api, worker") {
		t.Errorf("expected the error to list available services, got: %v", err)
	}
}

func TestDeploymentListServiceFlag(t *testing.T) {
	out, err := runListCmd(t, newFakeClientWithHistory(), "", "--service", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Service api") {
		t.Errorf("expected the service named by --service, got:\n%s", out)
	}
}

func TestDeploymentListServiceFlagBeatsPondYml(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pond.yml"), "version: 1\nname: api\n")
	t.Chdir(dir)

	out, err := runListCmd(t, newFakeClientWithHistory(), "", "--service", "worker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Service worker") {
		t.Errorf("expected --service to win over pond.yml, got:\n%s", out)
	}
}

func TestDeploymentListRejectsServiceFlagAndArgument(t *testing.T) {
	_, err := runListCmd(t, newFakeClientWithHistory(), "", "api", "--service", "worker")
	if err == nil {
		t.Fatal("expected an error when both an argument and --service are given")
	}
	if !strings.Contains(err.Error(), "not both") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDeploymentListServiceNameFromPondYml(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pond.yml"), "version: 1\nname: api\nimage: ghcr.io/acme/api\n")
	t.Chdir(dir)

	out, err := runListCmd(t, newFakeClientWithHistory(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Service api") {
		t.Errorf("expected the service name from pond.yml, got:\n%s", out)
	}
}
