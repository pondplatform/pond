package db

import (
	"testing"

	"github.com/pondplatform/pond/shared/server/api"
)

func TestDeployment_APIStatus(t *testing.T) {
	cases := []struct {
		state   DeploymentState
		started bool
		want    api.DeploymentStatus
	}{
		{DeploymentInProgress, false, api.DeploymentStatusPending},
		{DeploymentInProgress, true, api.DeploymentStatusRunning},
		{DeploymentSucceeded, true, api.DeploymentStatusSucceeded},
		{DeploymentFailed, true, api.DeploymentStatusFailed},
		{DeploymentCancelled, false, api.DeploymentStatusCancelled},
	}
	for _, tc := range cases {
		if got := (Deployment{Status: tc.state, Started: tc.started}).APIStatus(); got != tc.want {
			t.Errorf("%s started=%v: expected %s, got %s", tc.state, tc.started, tc.want, got)
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
