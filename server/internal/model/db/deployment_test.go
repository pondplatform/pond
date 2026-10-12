package db

import (
	"testing"

	"github.com/pondplatform/pond/shared/server/api"
)

func TestDeployment_APIStatus(t *testing.T) {
	cases := []struct {
		state             DeploymentState
		started, stopping bool
		want              api.DeploymentStatus
	}{
		{DeploymentInProgress, false, false, api.DeploymentStatusPending},
		{DeploymentInProgress, true, true, api.DeploymentStatusRunning},
		{DeploymentSucceeded, true, false, api.DeploymentStatusSucceeded},
		{DeploymentFailed, true, true, api.DeploymentStatusFailed},
		{DeploymentCancelled, true, true, api.DeploymentStatusCancelling},
		{DeploymentCancelled, true, false, api.DeploymentStatusCancelled},
	}
	for _, tc := range cases {
		if got := (Deployment{Status: tc.state, Started: tc.started, Stopping: tc.stopping}).APIStatus(); got != tc.want {
			t.Errorf("%s started=%v stopping=%v: expected %s, got %s", tc.state, tc.started, tc.stopping, tc.want, got)
		}
	}
}

func TestDependencyDeployment_APIStatus(t *testing.T) {
	if got := (DependencyDeployment{Managed: false}).APIStatus(nil); got != api.DependencyDeploymentStatusSucceeded {
		t.Errorf("an unmanaged dependency is ready from the start, got %s", got)
	}
	managed := DependencyDeployment{Managed: true}
	cases := map[CommandStatus]api.DependencyDeploymentStatus{
		CommandStatusQueued:     api.DependencyDeploymentStatusPending,
		CommandStatusDispatched: api.DependencyDeploymentStatusPending,
		CommandStatusSucceeded:  api.DependencyDeploymentStatusSucceeded,
		CommandStatusFailed:     api.DependencyDeploymentStatusFailed,
		CommandStatusCancelled:  api.DependencyDeploymentStatusCancelled,
	}
	for status, want := range cases {
		if got := managed.APIStatus(&Command{Status: status}); got != want {
			t.Errorf("command %s: expected %s, got %s", status, want, got)
		}
	}
}
