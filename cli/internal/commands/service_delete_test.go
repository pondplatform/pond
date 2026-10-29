package commands_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	"github.com/pondplatform/pond/cli/internal/commands"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
)

const (
	testDeleteProdID = "77777777-7777-7777-7777-777777777777"
	testDeleteDevID  = "88888888-8888-8888-8888-888888888888"
)

// deleteClient is the fake project with api live in prod and dev. Its
// deletes return one delete deployment per environment, whose GetDeployment
// answers with the outcome given for that environment.
type deleteClient struct {
	*fakeClient
	outcomes map[uuid.UUID]*api.Deployment
}

func newDeleteClient(prod, dev api.DeploymentStatus) *deleteClient {
	f := newFakeClient()
	devDeployment := f.deployments[uuid.MustParse(testEnvProdID)][0]
	devDeployment.ID, devDeployment.EnvironmentID = uuid.New(), uuid.MustParse(testEnvDevID)
	f.deployments[uuid.MustParse(testEnvDevID)] = []api.DeploymentListItem{devDeployment}

	c := &deleteClient{fakeClient: f, outcomes: map[uuid.UUID]*api.Deployment{}}
	for _, d := range []struct {
		id, env string
		status  api.DeploymentStatus
	}{{testDeleteDevID, testEnvDevID, dev}, {testDeleteProdID, testEnvProdID, prod}} {
		submitted := api.Deployment{ID: uuid.MustParse(d.id), EnvironmentID: uuid.MustParse(d.env), Kind: api.DeploymentKindDelete, Status: api.DeploymentStatusPending}
		f.deletes = append(f.deletes, submitted)
		outcome := submitted
		outcome.Status = d.status
		outcome.Commands = []api.CommandSummary{{ID: uuid.New(), Type: "service.delete", Status: commandStatusFor(d.status)}}
		if d.status == api.DeploymentStatusFailed {
			reason := "teardown failed: helm uninstall timed out"
			outcome.Error = &reason
			outcome.Commands[0].Error = "helm uninstall timed out"
		}
		c.outcomes[outcome.ID] = &outcome
	}
	return c
}

func commandStatusFor(status api.DeploymentStatus) api.CommandStatus {
	if status == api.DeploymentStatusFailed {
		return api.CommandStatusFailed
	}
	return api.CommandStatusSucceeded
}

func (c *deleteClient) GetDeployment(_ context.Context, id uuid.UUID) (*api.Deployment, error) {
	if d, ok := c.outcomes[id]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("deployment %s not found", id)
}

func runServiceDelete(t *testing.T, c client.ServerClient, env, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	commands.SetPollInterval(t, time.Millisecond)
	ctx := context.WithValue(context.Background(), pondconfig.ClientKey{}, c)
	ctx = context.WithValue(ctx, pondconfig.ResolvedConfigKey{}, pondconfig.ResolvedConfig{
		Server:  "http://localhost:8080",
		Project: testProjectID,
		Env:     env,
	})

	cmd := commands.NewServiceDeleteCmd()
	cmd.SilenceUsage = true
	cmd.SetContext(ctx)
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetIn(strings.NewReader(stdin))
	out, err := runCmd(t, cmd, args...)
	return out, errBuf.String(), err
}

func TestServiceDelete_YesSkipsTheConfirmation(t *testing.T) {
	c := newDeleteClient(api.DeploymentStatusSucceeded, api.DeploymentStatusSucceeded)
	out, _, err := runServiceDelete(t, c, "", "", "api", "--yes", "--keep-deps")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if len(c.deleted) != 1 || !c.deleted[0].KeepDependencies || c.deleted[0].TriggeredBy != "cli" {
		t.Fatalf("expected one delete keeping the dependencies, got %+v", c.deleted)
	}
	if strings.Contains(out, "Type the service name") {
		t.Errorf("--yes should not ask, got:\n%s", out)
	}
	assertInOrder(t, out, "Delete submitted: "+testDeleteDevID+" from dev", "Delete submitted: "+testDeleteProdID+" from prod", "Volumes are kept")
}

func TestServiceDelete_TypedNameConfirms(t *testing.T) {
	c := newDeleteClient(api.DeploymentStatusSucceeded, api.DeploymentStatusSucceeded)
	out, _, err := runServiceDelete(t, c, "", "api\n", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if len(c.deleted) != 1 || c.deleted[0].KeepDependencies {
		t.Fatalf("expected one delete destroying the dependencies, got %+v", c.deleted)
	}
	assertInOrder(t, out, "This deletes service api from dev, prod.", "their data is lost", "Volumes are kept.", "Type the service name to confirm:")
}

func TestServiceDelete_NoConfirmationDeletesNothing(t *testing.T) {
	for name, stdin := range map[string]string{"wrong name": "worker\n", "no input": ""} {
		t.Run(name, func(t *testing.T) {
			c := newDeleteClient(api.DeploymentStatusSucceeded, api.DeploymentStatusSucceeded)
			_, _, err := runServiceDelete(t, c, "", stdin, "api")
			if err == nil {
				t.Fatal("expected an error")
			}
			if len(c.deleted) != 0 {
				t.Errorf("expected no delete request, got %+v", c.deleted)
			}
			if stdin == "" && !strings.Contains(err.Error(), "--yes") {
				t.Errorf("expected a hint at --yes, got %v", err)
			}
		})
	}
}

func TestServiceDelete_WaitReportsEachEnvironment(t *testing.T) {
	c := newDeleteClient(api.DeploymentStatusSucceeded, api.DeploymentStatusSucceeded)
	out, _, err := runServiceDelete(t, c, "", "", "api", "--yes", "--wait")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	assertInOrder(t, out,
		"Deleting api from dev", "✓ Deleted api", "Delete succeeded",
		"Deleting api from prod", "✓ Deleted api", "Delete succeeded",
		"Service api deleted", "Volumes are kept")
}

func TestServiceDelete_WaitFailsWhenOneEnvironmentFails(t *testing.T) {
	c := newDeleteClient(api.DeploymentStatusFailed, api.DeploymentStatusSucceeded)
	out, _, err := runServiceDelete(t, c, "", "", "api", "--yes", "--wait")
	if err == nil || !strings.Contains(err.Error(), "prod: delete failed: teardown failed: helm uninstall timed out") {
		t.Fatalf("expected prod's failure, got %v\n%s", err, out)
	}
	if strings.Contains(out, "Service api deleted") {
		t.Errorf("must not claim the service was deleted, got:\n%s", out)
	}
	assertInOrder(t, out, "Deleting api from dev", "Delete succeeded", "Deleting api from prod", "✗ Deleting api failed")
}

func TestServiceDelete_AlreadyDeleted(t *testing.T) {
	c := newDeleteClient(api.DeploymentStatusSucceeded, api.DeploymentStatusSucceeded)
	c.services[0].Status = api.ServiceStatusDeleted
	out, _, err := runServiceDelete(t, c, "", "", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Service api is already deleted") || len(c.deleted) != 0 {
		t.Errorf("expected nothing to be deleted, got %+v:\n%s", c.deleted, out)
	}
}

func TestServiceDelete_SkipsEnvironmentsDeletedFrom(t *testing.T) {
	c := newDeleteClient(api.DeploymentStatusSucceeded, api.DeploymentStatusSucceeded)
	torn := &c.deployments[uuid.MustParse(testEnvDevID)][0]
	torn.Kind, torn.Status = api.DeploymentKindDelete, api.DeploymentStatusSucceeded
	out, _, err := runServiceDelete(t, c, "", "api\n", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "This deletes service api from prod.") {
		t.Errorf("expected only prod to be listed, got:\n%s", out)
	}
}

func TestServiceDelete_IgnoresTheEnvironmentOfTheContext(t *testing.T) {
	c := newDeleteClient(api.DeploymentStatusSucceeded, api.DeploymentStatusSucceeded)
	_, stderr, err := runServiceDelete(t, c, "prod", "", "api", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, `Ignoring environment "prod" from the context`) {
		t.Errorf("expected a note on stderr, got %q", stderr)
	}
}

func TestServiceDelete_RefusesPondEnv(t *testing.T) {
	t.Setenv(pondconfig.EnvEnvironment, "prod")
	c := newDeleteClient(api.DeploymentStatusSucceeded, api.DeploymentStatusSucceeded)
	_, _, err := runServiceDelete(t, c, "prod", "", "api", "--yes")
	if err == nil || !strings.Contains(err.Error(), "takes no environment") {
		t.Fatalf("expected POND_ENV to be refused, got %v", err)
	}
	if len(c.deleted) != 0 {
		t.Errorf("expected no delete request, got %+v", c.deleted)
	}
}

func TestStatusLabelsADelete(t *testing.T) {
	c := newDeleteClient(api.DeploymentStatusSucceeded, api.DeploymentStatusSucceeded)
	c.services[0].Status = api.ServiceStatusDeleting
	torn := &c.deployments[uuid.MustParse(testEnvDevID)][0]
	torn.Kind, torn.Status = api.DeploymentKindDelete, api.DeploymentStatusSucceeded
	failed := &c.deployments[uuid.MustParse(testEnvProdID)][0]
	failed.Kind, failed.Status = api.DeploymentKindDelete, api.DeploymentStatusFailed

	out, err := runStatusCmd(t, c, "", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertInOrder(t, out, "Service api (deleting)", "dev", "deleted", "prod", "delete failed")
}
