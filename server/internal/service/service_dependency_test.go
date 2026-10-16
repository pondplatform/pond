package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

func TestResolveServiceDependencies(t *testing.T) {
	backendID := uuid.New()
	exports := map[string]string{"host": "backend.staging.svc.cluster.local", "port": "8080"}
	repos := func(deployments []db.Deployment) store.Repos {
		return store.Repos{
			Services: &testutil.MockServiceRepository{
				GetByNameFn: func(_ context.Context, _ uuid.UUID, name string) (*db.Service, error) {
					if name != "backend" {
						return nil, api.NotFound("service %q not found", name)
					}
					return &db.Service{ID: backendID, Name: name}, nil
				},
			},
			Deployments: &testutil.MockDeploymentInfoStore{
				ListByServiceFilteredFn: func(_ context.Context, serviceID uuid.UUID, envID *uuid.UUID, status *api.DeploymentStatus, limit int, _ string) ([]db.Deployment, error) {
					if serviceID != backendID || *envID != testEnv.ID || *status != api.DeploymentStatusSucceeded || limit != 1 {
						return nil, errors.New("unexpected lookup")
					}
					return deployments, nil
				},
			},
		}
	}
	consumer := func(target string) *serviceconfig.ServiceConfig {
		return &serviceconfig.ServiceConfig{
			Name: "web",
			Dependencies: map[string]serviceconfig.DependencyDeclaration{
				"api": {Type: serviceconfig.DependencyTypeService, Config: map[string]any{"service": target}},
				"db":  {Type: "postgres"},
			},
		}
	}
	dbInput := map[string]api.DependencyInput{"db": {Managed: true}}

	t.Run("imports the exports of the latest succeeded deployment", func(t *testing.T) {
		got, err := resolveServiceDependencies(context.Background(), repos([]db.Deployment{{Exports: exports}}), uuid.New(), &testEnv, consumer("backend"), dbInput)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := map[string]api.DependencyInput{
			"db":  {Managed: true},
			"api": {Values: map[string]any{"host": "backend.staging.svc.cluster.local", "port": "8080"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("expected %+v, got %+v", want, got)
		}
		if _, ok := dbInput["api"]; ok {
			t.Error("the requested inputs were modified")
		}
	})

	problems := map[string]struct {
		target      string
		requested   map[string]api.DependencyInput
		deployments []db.Deployment
		want        string
	}{
		"unknown service":          {target: "nope", want: `service "nope" not found`},
		"never deployed here":      {target: "backend", want: `no successful deployment in environment "staging"`},
		"deployed before exports":  {target: "backend", deployments: []db.Deployment{{}}, want: "redeploy it first"},
		"depends on itself":        {target: "web", want: `names service "web" itself`},
		"given input in a request": {target: "backend", requested: map[string]api.DependencyInput{"api": {}}, want: "takes no input"},
	}
	for name, tc := range problems {
		t.Run(name, func(t *testing.T) {
			_, err := resolveServiceDependencies(context.Background(), repos(tc.deployments), uuid.New(), &testEnv, consumer(tc.target), tc.requested)
			var invalid *api.ValidationErrors
			if !errors.As(err, &invalid) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected a validation error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestVerifyUnmanagedReferences_PrivateVariable(t *testing.T) {
	cfg := &serviceconfig.ServiceConfig{
		Dependencies: map[string]serviceconfig.DependencyDeclaration{
			"api": {Type: serviceconfig.DependencyTypeService, Config: map[string]any{"service": "backend"}},
		},
		Env: map[string]string{"KEY": "{{dependencies.api.variables.API_KEY}}"},
	}
	inputs := map[string]api.DependencyInput{"api": {Values: map[string]any{"host": "backend"}}}
	err := verifyUnmanagedReferences(cfg, inputs)
	if err == nil || !strings.Contains(err.Error(), `service "backend" has no public variable "API_KEY"`) {
		t.Errorf("expected the private variable to be reported, got %v", err)
	}
}

func TestVerifyUnmanagedReferences_ServiceWithoutGateway(t *testing.T) {
	cfg := &serviceconfig.ServiceConfig{
		Dependencies: map[string]serviceconfig.DependencyDeclaration{
			"api": {Type: serviceconfig.DependencyTypeService, Config: map[string]any{"service": "backend"}},
		},
		Env: map[string]string{"PUBLIC_API": "{{dependencies.api.gateway.url}}"},
	}
	inputs := map[string]api.DependencyInput{"api": {Values: map[string]any{"host": "backend"}}}
	err := verifyUnmanagedReferences(cfg, inputs)
	if err == nil || !strings.Contains(err.Error(), `service "backend" exports no "gateway.url"`) {
		t.Errorf("expected the missing gateway export to be reported, got %v", err)
	}
}
