package service

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
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

func TestPlan_ProvisionsManagedDependenciesThenRollsOut(t *testing.T) {
	deployment := planTestDeployment(map[string]string{"db": "postgres", "api": "http-service"}, nil)
	deps, cmds, err := plan(deployment, testEnv, map[string]api.DependencyInput{
		"db":  {Managed: true, Values: map[string]any{"instances": 1}},
		"api": {Values: map[string]any{"url": "http://api"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmds) != 2 || cmds[0].Type != db.CommandTypeTofuApply || cmds[0].Phase != phaseDependencies ||
		cmds[1].Type != db.CommandTypeHelmUpgrade || cmds[1].Phase != phaseRollout {
		t.Fatalf("expected a tofu command in phase 0 and helm in phase 1, got %+v", cmds)
	}
	if cmds[0].Payload == nil || cmds[1].Payload != nil {
		t.Errorf("expected tofu ready to run and helm left for its phase")
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
	if firstPhase(cmds) != phaseDependencies {
		t.Errorf("expected the dependencies' phase to run first")
	}
}

func TestPlan_WithoutManagedDependenciesStartsWithTheRollout(t *testing.T) {
	_, cmds, err := plan(planTestDeployment(nil, nil), testEnv, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmds) != 1 || firstPhase(cmds) != phaseRollout {
		t.Errorf("expected only the rollout, got %+v", cmds)
	}
}

func TestActivate_RendersTheRolloutFromDependencyOutputs(t *testing.T) {
	deployment := planTestDeployment(map[string]string{"db": "postgres", "api": "http-service"},
		map[string]any{"DB_HOST": "{{db.host}}", "API_URL": "{{api.url}}"})
	deps, cmds, _ := plan(deployment, testEnv, map[string]api.DependencyInput{
		"db":  {Managed: true},
		"api": {Values: map[string]any{"url": "http://api"}},
	})
	cmds[0].Status = db.CommandStatusSucceeded
	cmds[0].Output = json.RawMessage(`{"host":"db.internal"}`)

	changed, err := activate(deployment, testEnv, deps, cmds, phaseRollout, helmgen.NewGenerator())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changed) != 1 || changed[0] != cmds[1] {
		t.Fatalf("expected the helm command rendered, got %+v", changed)
	}
	var payload wire.HelmUpgradePayload
	if err := json.Unmarshal(cmds[1].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var values struct {
		Configs []struct {
			Data string `yaml:"data"`
		} `yaml:"configs"`
	}
	if err := yaml.Unmarshal(payload.Values, &values); err != nil || len(values.Configs) != 1 {
		t.Fatalf("decode helm values %s: %v", payload.Values, err)
	}
	file, _ := base64.StdEncoding.DecodeString(values.Configs[0].Data)
	if !strings.Contains(string(file), "db.internal") || !strings.Contains(string(file), "http://api") {
		t.Errorf("expected both outputs in the rendered config, got %q", file)
	}
}

func TestActivate_FailsOnAReferenceThatDoesNotResolve(t *testing.T) {
	deployment := planTestDeployment(map[string]string{"db": "postgres"}, map[string]any{"DB_USER": "{{db.username}}"})
	deps, cmds, _ := plan(deployment, testEnv, map[string]api.DependencyInput{"db": {Managed: true}})
	cmds[0].Status = db.CommandStatusSucceeded
	cmds[0].Output = json.RawMessage(`{"host":"db.internal"}`)

	if _, err := activate(deployment, testEnv, deps, cmds, phaseRollout, helmgen.NewGenerator()); err == nil || !strings.Contains(err.Error(), "db.username") {
		t.Errorf("expected an error naming db.username, got %v", err)
	}
}

func TestAfterResult(t *testing.T) {
	dbID, otherID, helmID := uuid.New(), uuid.New(), uuid.New()
	deps := []db.DependencyDeployment{
		{DependencyName: "db", Managed: true, CommandID: &dbID},
		{DependencyName: "other", Managed: true, CommandID: &otherID},
	}
	commands := func(db0, other db.CommandStatus, dbOutput string) []*db.Command {
		return []*db.Command{
			{ID: dbID, Type: db.CommandTypeTofuApply, Phase: phaseDependencies, Status: db0, Output: json.RawMessage(dbOutput), Error: "exit status 1"},
			{ID: otherID, Type: db.CommandTypeTofuApply, Phase: phaseDependencies, Status: other, Output: json.RawMessage(`{}`)},
			{ID: helmID, Type: db.CommandTypeHelmUpgrade, Phase: phaseRollout, Status: db.CommandStatusQueued, Error: "timed out"},
		}
	}

	cases := map[string]struct {
		cmds      []*db.Command
		finished  uuid.UUID
		failure   string
		done      bool
		nextPhase int
	}{
		"a failed dependency fails the deployment": {
			cmds: commands(db.CommandStatusFailed, db.CommandStatusDispatched, ``), finished: dbID,
			failure: `dependency "db" failed: exit status 1`,
		},
		"tofu outputs that are not an object fail it": {
			cmds: commands(db.CommandStatusSucceeded, db.CommandStatusDispatched, `[1]`), finished: dbID,
			failure: `dependency "db": tofu outputs must be a JSON object, got [1]`,
		},
		"a phase still running waits": {
			cmds: commands(db.CommandStatusSucceeded, db.CommandStatusDispatched, `{}`), finished: dbID,
			nextPhase: -1,
		},
		"a finished phase starts the next one": {
			cmds: commands(db.CommandStatusSucceeded, db.CommandStatusSucceeded, `{}`), finished: dbID,
			nextPhase: phaseRollout,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := afterResult(commandByID(tc.cmds, &tc.finished), tc.cmds, deps)
			if got.failure != tc.failure || got.done != tc.done {
				t.Fatalf("expected failure %q and done %v, got %+v", tc.failure, tc.done, got)
			}
			if tc.nextPhase == -1 && got.start != nil || tc.nextPhase > 0 && (got.start == nil || *got.start != tc.nextPhase) {
				t.Errorf("expected next phase %d, got %+v", tc.nextPhase, got.start)
			}
		})
	}

	t.Run("the last phase finishing completes the deployment", func(t *testing.T) {
		cmds := commands(db.CommandStatusSucceeded, db.CommandStatusSucceeded, `{}`)
		cmds[2].Status = db.CommandStatusSucceeded
		if got := afterResult(cmds[2], cmds, deps); !got.done {
			t.Errorf("expected done, got %+v", got)
		}
	})

	t.Run("a failed rollout fails the deployment", func(t *testing.T) {
		cmds := commands(db.CommandStatusSucceeded, db.CommandStatusSucceeded, `{}`)
		cmds[2].Status = db.CommandStatusFailed
		if got := afterResult(cmds[2], cmds, deps); got.failure != "helm upgrade failed: timed out" {
			t.Errorf("expected the helm failure, got %+v", got)
		}
	})
}
