package service

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

var testEnv = db.Environment{ID: uuid.New(), ClusterID: uuid.New(), Name: "staging", Namespace: "staging"}

func planTestDeployment(deps map[string]string, configValues map[string]any) db.Deployment {
	declared := make(map[string]serviceconfig.DependencyDeclaration, len(deps))
	for name, typ := range deps {
		declared[name] = serviceconfig.DependencyDeclaration{Type: typ}
	}
	d := db.Deployment{
		ID:            uuid.New(),
		EnvironmentID: testEnv.ID,
		ImageTag:      "v1",
		Status:        db.DeploymentInProgress,
		ServiceConfigSnapshot: serviceconfig.ServiceConfig{
			Name:         "svc",
			Image:        "ghcr.io/example/svc",
			Dependencies: declared,
		},
	}
	if configValues != nil {
		d.ServiceConfigSnapshot.Configs = map[string]serviceconfig.ConfigFileSpec{
			"app.env": {Format: ".env", MountDir: "/etc/app", Values: configValues},
		}
	}
	return d
}

func TestResolveInputs(t *testing.T) {
	declared := map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}, "api": {Type: "http-service"}}
	previous := map[string]db.DependencyDeployment{
		"db":  {DependencyName: "db", DependencyType: "postgres", Managed: true, ProviderInputs: map[string]any{"instances": 2}},
		"api": {DependencyName: "api", DependencyType: "http-service", UserConfig: map[string]any{"url": "http://old"}},
	}

	t.Run("the request wins, the rest is reused", func(t *testing.T) {
		got, err := resolveInputs(declared, map[string]api.DependencyInput{"api": {Values: map[string]any{"url": "http://new"}}}, previous)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["api"].Values["url"] != "http://new" {
			t.Errorf("expected the requested api input, got %+v", got["api"])
		}
		if !got["db"].Managed || got["db"].Values["instances"] != 2 {
			t.Errorf("expected the previous db input, got %+v", got["db"])
		}
	})

	t.Run("a dependency whose type changed is not reused", func(t *testing.T) {
		retyped := map[string]db.DependencyDeployment{"db": {DependencyName: "db", DependencyType: "mysql", Managed: true}}
		_, err := resolveInputs(declared, map[string]api.DependencyInput{"api": {}}, retyped)
		if got := validationErrors(t, err); len(got) != 1 || got[0].Field != "db" || !strings.Contains(got[0].Message, "missing input") {
			t.Errorf("expected db to miss input, got %+v", got)
		}
	})

	t.Run("nothing to reuse", func(t *testing.T) {
		_, err := resolveInputs(declared, nil, nil)
		if got := validationErrors(t, err); len(got) != 2 {
			t.Errorf("expected both dependencies to miss input, got %+v", got)
		}
	})
}

func TestPlan_ProvisionsEveryManagedDependency(t *testing.T) {
	deployment := planTestDeployment(map[string]string{"db": "postgres", "api": "http-service"}, nil)
	deps, cmds, err := plan(deployment, testEnv, map[string]api.DependencyInput{
		"db":  {Managed: true, Values: map[string]any{"instances": 1}},
		"api": {Values: map[string]any{"url": "http://api"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Type != db.CommandTypeTofuApply || cmds[0].Status != db.CommandStatusQueued || len(cmds[0].Payload) == 0 {
		t.Fatalf("expected one queued tofu command ready to run, got %+v", cmds)
	}
	for _, dep := range deps {
		switch dep.DependencyName {
		case "db":
			if !dep.Managed || dep.CommandID == nil || *dep.CommandID != cmds[0].ID {
				t.Errorf("expected db managed and linked to the tofu command, got %+v", dep)
			}
		case "api":
			if dep.Managed || dep.CommandID != nil || dep.UserConfig["url"] != "http://api" {
				t.Errorf("expected api unmanaged with its config, got %+v", dep)
			}
		}
	}
}

func TestPlan_WithoutManagedDependenciesQueuesNothing(t *testing.T) {
	_, cmds, err := plan(planTestDeployment(map[string]string{"api": "http-service"}, nil), testEnv,
		map[string]api.DependencyInput{"api": {Values: map[string]any{"url": "http://api"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmds) != 0 {
		t.Errorf("expected no commands; the rollout is the state machine's, got %+v", cmds)
	}
}
