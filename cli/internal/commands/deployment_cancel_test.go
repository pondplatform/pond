package commands_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	"github.com/pondplatform/pond/cli/internal/commands"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
)

// newCancelClient is the fake project whose latest deployment of api in prod
// has the given status and commands.
func newCancelClient(status api.DeploymentStatus, cmds ...api.CommandSummary) *fakeClient {
	f := newFakeClient()
	f.deployments[uuid.MustParse(testEnvProdID)][0].Status = status
	f.detail = waitPoll(status, cmds...).deployment
	return f
}

func runCancel(t *testing.T, c client.ServerClient, env, stdin string, args ...string) (string, error) {
	t.Helper()
	ctx := context.WithValue(context.Background(), pondconfig.ClientKey{}, c)
	ctx = context.WithValue(ctx, pondconfig.ResolvedConfigKey{}, pondconfig.ResolvedConfig{
		Server:  "http://localhost:8080",
		Project: testProjectID,
		Env:     env,
	})

	cmd := commands.NewDeploymentCancelCmd()
	cmd.SilenceUsage = true
	cmd.SetContext(ctx)
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader(stdin))
	return runCmd(t, cmd, args...)
}

func TestDeploymentCancel_ConfirmedCancelsTheDeploymentInProgress(t *testing.T) {
	c := newCancelClient(api.DeploymentStatusRunning,
		tofuCmd(testDBCommandID, api.CommandStatusDispatched, 0),
		tofuCmd(testCacheCommandID, api.CommandStatusQueued, 0))
	out, err := runCancel(t, c, "prod", "y\n", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if len(c.cancelled) != 1 || c.cancelled[0].String() != testDeployID {
		t.Fatalf("expected deployment %s cancelled, got %v", testDeployID, c.cancelled)
	}
	assertInOrder(t, out,
		"This cancels the deployment of service api to prod:\n",
		"  Deployment: "+testDeployID+"\n",
		"  Status:     running\n",
		"  Step:       [1/3] Provisioning db (postgres)\n",
		"  Image:      v1.2.3\n",
		"  Triggered:  cli\n",
		"  Created:    2026-09-12 10:30:00\n",
		"Cancel it? [y/N]: ",
		"Deployment "+testDeployID+" cancelled\n",
		"The agent is told to stop provisioning db (postgres); the deployment shows as cancelling until it has.\n",
	)
}

func TestDeploymentCancel_YesSkipsTheConfirmation(t *testing.T) {
	c := newCancelClient(api.DeploymentStatusPending, tofuCmd(testDBCommandID, api.CommandStatusQueued, 0))
	out, err := runCancel(t, c, "prod", "", "api", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if len(c.cancelled) != 1 {
		t.Fatalf("expected one cancel, got %v", c.cancelled)
	}
	// Nothing was running, so nothing is left for the agent to stop.
	if want := "Deployment " + testDeployID + " cancelled\n"; out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestDeploymentCancel_NoConfirmationCancelsNothing(t *testing.T) {
	for name, stdin := range map[string]string{"no": "n\n", "anything else": "cancel\n", "no input": ""} {
		t.Run(name, func(t *testing.T) {
			c := newCancelClient(api.DeploymentStatusRunning, tofuCmd(testDBCommandID, api.CommandStatusDispatched, 0))
			_, err := runCancel(t, c, "prod", stdin, "api")
			if err == nil {
				t.Fatal("expected an error")
			}
			if len(c.cancelled) != 0 {
				t.Errorf("expected no cancel, got %v", c.cancelled)
			}
			if stdin == "" && !strings.Contains(err.Error(), "--yes") {
				t.Errorf("expected a hint at --yes, got %v", err)
			}
		})
	}
}

func TestDeploymentCancel_DeleteInProgress(t *testing.T) {
	c := newCancelClient(api.DeploymentStatusRunning,
		api.CommandSummary{ID: uuid.New(), Type: "service.delete", Status: api.CommandStatusDispatched})
	c.detail.Kind, c.detail.ImageTag = api.DeploymentKindDelete, ""
	out, err := runCancel(t, c, "prod", "yes\n", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	assertInOrder(t, out,
		"This cancels the delete of service api from prod:\n",
		"  Status:     deleting\n",
		"  Step:       [1/1] Deleting api\n",
		"Delete "+testDeployID+" cancelled\n",
		"The agent is told to stop deleting api; the delete shows as cancelling until it has.\n",
	)
	if strings.Contains(out, "Image:") {
		t.Errorf("a delete without an image should not show one, got:\n%s", out)
	}
}

func TestDeploymentCancel_ByDeploymentIDDoesNotAsk(t *testing.T) {
	c := newCancelClient(api.DeploymentStatusRunning, helmCmd(api.CommandStatusDispatched, 0))
	out, err := runCancel(t, c, "", "", "--deployment-id", testDeployID)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if len(c.cancelled) != 1 {
		t.Fatalf("expected one cancel, got %v", c.cancelled)
	}
	if strings.Contains(out, "Cancel it?") {
		t.Errorf("--deployment-id should not ask, got:\n%s", out)
	}
	assertInOrder(t, out, "Deployment "+testDeployID+" cancelled\n", "stop rolling out the service;")
}

func TestDeploymentCancel_NothingInProgress(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status api.DeploymentStatus
		args   []string
	}{
		{"service", api.DeploymentStatusSucceeded, []string{"api", "--yes"}},
		{"deployment id", api.DeploymentStatusCancelling, []string{"--deployment-id", testDeployID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCancelClient(tc.status)
			_, err := runCancel(t, c, "prod", "", tc.args...)
			want := "nothing to cancel: deployment " + testDeployID + " is not in progress (status: " + string(tc.status) + ")"
			if err == nil || err.Error() != want {
				t.Fatalf("expected %q, got %v", want, err)
			}
			if len(c.cancelled) != 0 {
				t.Errorf("expected no cancel, got %v", c.cancelled)
			}
		})
	}
}

func TestDeploymentCancel_NeverDeployed(t *testing.T) {
	_, err := runCancel(t, newFakeClient(), "dev", "", "api", "--yes")
	if err == nil || !strings.Contains(err.Error(), "service api has never been deployed to dev") {
		t.Fatalf("expected a never-deployed error, got %v", err)
	}
}

func TestDeploymentCancel_NeedsEnvironmentOrDeploymentID(t *testing.T) {
	_, err := runCancel(t, newFakeClient(), "", "", "api")
	if err == nil || !strings.Contains(err.Error(), "--deployment-id") {
		t.Fatalf("expected a hint at --deployment-id, got %v", err)
	}
}

func TestDeploymentCancel_RejectsServiceAndDeploymentID(t *testing.T) {
	_, err := runCancel(t, newFakeClient(), "prod", "", "api", "--deployment-id", testDeployID)
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected an either-or error, got %v", err)
	}
}
