package db

import (
	"testing"

	"github.com/pondplatform/pond/shared/server/api"
)

func TestServiceStatus(t *testing.T) {
	deployed := Deployment{Kind: DeploymentKindDeploy, Status: DeploymentSucceeded}
	deploying := Deployment{Kind: DeploymentKindDeploy, Status: DeploymentInProgress}
	deleting := Deployment{Kind: DeploymentKindDelete, Status: DeploymentInProgress}
	deleted := Deployment{Kind: DeploymentKindDelete, Status: DeploymentSucceeded}
	deleteFailed := Deployment{Kind: DeploymentKindDelete, Status: DeploymentFailed}
	cases := map[string]struct {
		latest []Deployment
		want   api.ServiceStatus
	}{
		"never deployed":                {nil, api.ServiceStatusActive},
		"deployed":                      {[]Deployment{deployed}, api.ServiceStatusActive},
		"redeployed after a delete":     {[]Deployment{deploying}, api.ServiceStatusActive},
		"deleting from one environment": {[]Deployment{deleted, deleting}, api.ServiceStatusDeleting},
		"deleting wins over a failure":  {[]Deployment{deleteFailed, deleting}, api.ServiceStatusDeleting},
		"deleted from every one":        {[]Deployment{deleted, deleted}, api.ServiceStatusDeleted},
		"still deployed in one":         {[]Deployment{deleted, deployed}, api.ServiceStatusActive},
		"a delete failed in one":        {[]Deployment{deleted, deleteFailed}, api.ServiceStatusActive},
	}
	for name, tc := range cases {
		if got := ServiceStatus(tc.latest); got != tc.want {
			t.Errorf("%s: expected %s, got %s", name, tc.want, got)
		}
	}
}
