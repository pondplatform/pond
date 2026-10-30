package commands_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	"github.com/pondplatform/pond/cli/internal/commands"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
)

// newServiceListClient is the fake project with a third service, gone, that
// was deleted from prod, and services out of name order.
func newServiceListClient() *fakeClient {
	f := newFakeClient()
	f.services[0].Status = api.ServiceStatusActive
	f.services[0].CreatedAt = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f.services[1].Status = api.ServiceStatusActive

	gone := api.Service{ID: uuid.New(), ProjectID: uuid.MustParse(testProjectID), Name: "gone", Status: api.ServiceStatusDeleted}
	f.services = append([]api.Service{f.services[1], gone}, f.services[0])

	prod := uuid.MustParse(testEnvProdID)
	f.deployments[prod] = append(f.deployments[prod], api.DeploymentListItem{
		ID:            uuid.New(),
		ServiceID:     gone.ID,
		EnvironmentID: prod,
		Kind:          api.DeploymentKindDelete,
		Status:        api.DeploymentStatusSucceeded,
		ImageTag:      "v0.9.0",
		CreatedAt:     time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
	})
	return f
}

func runServiceListCmd(t *testing.T, c client.ServerClient, env string, args ...string) (string, error) {
	t.Helper()
	ctx := context.WithValue(context.Background(), pondconfig.ClientKey{}, c)
	ctx = context.WithValue(ctx, pondconfig.ResolvedConfigKey{}, pondconfig.ResolvedConfig{
		Server:  "http://localhost:8080",
		Project: testProjectID,
		Env:     env,
	})

	cmd := commands.NewServiceListCmd()
	cmd.SilenceUsage = true
	cmd.SetContext(ctx)
	return runCmd(t, cmd, args...)
}

func TestServiceListShowsEveryServiceWithItsStatus(t *testing.T) {
	out, err := runServiceListCmd(t, newServiceListClient(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	apiRow, goneRow, workerRow := lineContaining(t, out, "api"), lineContaining(t, out, "gone"), lineContaining(t, out, "worker")
	if strings.Index(out, apiRow) > strings.Index(out, goneRow) || strings.Index(out, goneRow) > strings.Index(out, workerRow) {
		t.Errorf("expected services sorted by name, got:\n%s", out)
	}
	if !strings.Contains(apiRow, "active") || !strings.Contains(apiRow, "2026-09-01 12:00:00") {
		t.Errorf("expected api's status and creation time, got: %q", apiRow)
	}
	// A deleted service is listed, not hidden.
	if !strings.Contains(goneRow, "deleted") {
		t.Errorf("expected the deleted service to be listed as deleted, got: %q", goneRow)
	}
	if strings.Contains(out, "ENVIRONMENT") || strings.Contains(out, "IMAGE") {
		t.Errorf("expected no per-environment columns without an environment, got:\n%s", out)
	}
}

func TestServiceListSingleEnvironmentShowsLatestDeployments(t *testing.T) {
	out, err := runServiceListCmd(t, newServiceListClient(), "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "Environment prod") {
		t.Errorf("expected the environment in the header, got:\n%s", out)
	}
	apiRow := lineContaining(t, out, "api")
	if !strings.Contains(apiRow, "succeeded") || !strings.Contains(apiRow, "v1.2.3") || !strings.Contains(apiRow, testDeployID) {
		t.Errorf("expected api's latest prod deployment, got: %q", apiRow)
	}
	if gone := lineContaining(t, out, "gone"); !strings.Contains(gone, "deleted") {
		t.Errorf("expected the delete to read as deleted, got: %q", gone)
	}
	// worker was never deployed to prod.
	if worker := strings.Fields(lineContaining(t, out, "worker")); len(worker) != 5 || worker[1] != "-" {
		t.Errorf("expected placeholders for a service never deployed to prod, got: %q", worker)
	}
}

func TestServiceListUnknownEnvironment(t *testing.T) {
	_, err := runServiceListCmd(t, newServiceListClient(), "staging")
	if err == nil {
		t.Fatal("expected an error for an unknown environment")
	}
	if !strings.Contains(err.Error(), "staging") || !strings.Contains(err.Error(), "prod") {
		t.Errorf("expected the error to name the environment and the available ones, got: %v", err)
	}
}

func TestServiceListWithoutServices(t *testing.T) {
	f := newFakeClient()
	f.services = nil

	for _, env := range []string{"", "prod"} {
		out, err := runServiceListCmd(t, f, env)
		if err != nil {
			t.Fatalf("env %q: unexpected error: %v", env, err)
		}
		if !strings.Contains(out, "No services yet") {
			t.Errorf("env %q: expected a no-services notice, got:\n%s", env, out)
		}
	}
}

func TestServiceListRejectsArguments(t *testing.T) {
	if _, err := runServiceListCmd(t, newServiceListClient(), "", "api"); err == nil {
		t.Fatal("expected an error for a positional argument")
	}
}
