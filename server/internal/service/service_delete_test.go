package service

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// teardownFixture is a delete deployment: no dependencies, and at most its
// service.delete command.
func teardownFixture() *machineFixture {
	d := planTestDeployment(nil, nil)
	d.ServiceID = uuid.New()
	d.Kind = db.DeploymentKindDelete
	return &machineFixture{deployment: d}
}

func (f *machineFixture) withTeardown(status db.CommandStatus, errMsg string) *machineFixture {
	f.commands = append(f.commands, &db.Command{ID: uuid.New(), Type: db.CommandTypeServiceDelete, Status: status, Error: errMsg})
	return f
}

func TestCurrentState_Delete(t *testing.T) {
	cases := map[string]struct {
		fixture *machineFixture
		want    deploymentState
	}{
		"teardown queued":      {teardownFixture().withTeardown(db.CommandStatusQueued, ""), stateRollingOut},
		"teardown running":     {teardownFixture().withTeardown(db.CommandStatusDispatched, ""), stateRollingOut},
		"teardown failed":      {teardownFixture().withTeardown(db.CommandStatusFailed, "x"), stateRolloutFailed},
		"teardown cancelled":   {teardownFixture().withTeardown(db.CommandStatusCancelled, ""), stateRolloutFailed},
		"teardown succeeded":   {teardownFixture().withTeardown(db.CommandStatusSucceeded, ""), stateRolledOut},
		"nothing to tear down": {teardownFixture(), stateRolledOut},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.fixture.state(); got != tc.want {
				t.Errorf("expected state %d, got %d", tc.want, got)
			}
		})
	}
}

func TestAdvance_Delete(t *testing.T) {
	t.Run("a failed teardown fails the delete", func(t *testing.T) {
		w := teardownFixture().withTeardown(db.CommandStatusFailed, "helm uninstall timed out").advance(t)
		if w.saved == nil || w.saved.Status != db.DeploymentFailed || reasonOf(w.saved) != "teardown failed: helm uninstall timed out" {
			t.Errorf("expected failed with the teardown's reason, got %+v", w.saved)
		}
	})
	t.Run("a cancelled teardown fails the delete", func(t *testing.T) {
		w := teardownFixture().withTeardown(db.CommandStatusCancelled, "").advance(t)
		if w.saved == nil || reasonOf(w.saved) != "teardown was cancelled" {
			t.Errorf("expected the cancellation as reason, got %+v", w.saved)
		}
	})
	for name, f := range map[string]*machineFixture{
		"a succeeded teardown": teardownFixture().withTeardown(db.CommandStatusSucceeded, ""),
		"nothing to tear down": teardownFixture(),
	} {
		t.Run(name+" completes the delete", func(t *testing.T) {
			w := f.advance(t)
			if w.saved == nil || w.saved.Status != db.DeploymentSucceeded {
				t.Errorf("expected succeeded, got %+v", w.saved)
			}
			if len(w.created) != 0 {
				t.Errorf("a delete must never roll out, created %+v", w.created)
			}
		})
	}
	t.Run("a running teardown writes nothing", func(t *testing.T) {
		if w := teardownFixture().withTeardown(db.CommandStatusDispatched, "").advance(t); w.saved != nil || len(w.created) != 0 {
			t.Errorf("expected no writes, got %+v", w)
		}
	})
}

func historyCommand(t *testing.T, cluster uuid.UUID, cmdType string, payload any) *db.Command {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return &db.Command{ID: uuid.New(), ClusterID: cluster, Type: cmdType, Payload: raw}
}

func applyOf(dep string, extra string) wire.TofuApplyPayload {
	return wire.TofuApplyPayload{
		WorkDir:   "/opt/pond/tofu-providers/postgres",
		StatePath: "/states/env/api/" + dep + "/terraform.tfstate",
		Vars:      map[string]any{"dependency_name": dep, "extra": extra},
	}
}

func decodeTeardown(t *testing.T, cmd *db.Command) wire.ServiceDeletePayload {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a teardown command, got nil")
	}
	if cmd.Type != db.CommandTypeServiceDelete || cmd.Status != db.CommandStatusQueued {
		t.Fatalf("unexpected command %+v", cmd)
	}
	var payload wire.ServiceDeletePayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestTeardownCommand(t *testing.T) {
	oldCluster, cluster := uuid.New(), uuid.New()
	deploymentID := uuid.New()
	upgrade := func(c uuid.UUID, ns string) *db.Command {
		return historyCommand(t, c, db.CommandTypeHelmUpgrade, wire.HelmUpgradePayload{ReleaseName: "api", Namespace: ns, Values: []byte("x: 1")})
	}
	apply := func(c uuid.UUID, dep, extra string) *db.Command {
		return historyCommand(t, c, db.CommandTypeTofuApply, applyOf(dep, extra))
	}
	history := []*db.Command{
		apply(oldCluster, "old", "1"), upgrade(oldCluster, "old-ns"),
		apply(cluster, "db", "1"), apply(cluster, "cache", "1"), upgrade(cluster, "staging"),
		apply(cluster, "db", "2"), upgrade(cluster, "prod"),
	}

	t.Run("the latest rollout and every dependency on its cluster", func(t *testing.T) {
		cmd, err := teardownCommand(deploymentID, history, false)
		if err != nil {
			t.Fatal(err)
		}
		if cmd.ClusterID != cluster || cmd.DeploymentID != deploymentID {
			t.Errorf("expected the latest rollout's cluster and the delete's ID, got %+v", cmd)
		}
		payload := decodeTeardown(t, cmd)
		if payload.Release == nil || *payload.Release != (wire.HelmRelease{ReleaseName: "api", Namespace: "prod"}) {
			t.Errorf("expected release api in prod, got %+v", payload.Release)
		}
		var names []string
		for _, dep := range payload.Dependencies {
			names = append(names, dep.Name)
		}
		if !slices.Equal(names, []string{"cache", "db"}) {
			t.Fatalf("expected cache and db by state path, without the other cluster's, got %q", names)
		}
		got := payload.Dependencies[1]
		want := applyOf("db", "2")
		if got.WorkDir != want.WorkDir || got.StatePath != want.StatePath || got.Vars["extra"] != "2" {
			t.Errorf("expected the latest apply of db, verbatim, got %+v", got)
		}
	})
	t.Run("keepDeps only uninstalls", func(t *testing.T) {
		cmd, err := teardownCommand(deploymentID, history, true)
		if err != nil {
			t.Fatal(err)
		}
		if payload := decodeTeardown(t, cmd); payload.Release == nil || len(payload.Dependencies) != 0 {
			t.Errorf("expected only the release, got %+v", payload)
		}
	})
	t.Run("never rolled out: dependencies only", func(t *testing.T) {
		cmd, err := teardownCommand(deploymentID, []*db.Command{apply(cluster, "db", "1")}, false)
		if err != nil {
			t.Fatal(err)
		}
		if payload := decodeTeardown(t, cmd); payload.Release != nil || len(payload.Dependencies) != 1 || cmd.ClusterID != cluster {
			t.Errorf("expected only db on its apply's cluster, got %+v", payload)
		}
	})
	for name, tc := range map[string]struct {
		history  []*db.Command
		keepDeps bool
	}{
		"no commands":             {nil, false},
		"dependencies only, kept": {[]*db.Command{apply(cluster, "db", "1")}, true},
	} {
		t.Run(name+": nothing to remove", func(t *testing.T) {
			if cmd, err := teardownCommand(deploymentID, tc.history, tc.keepDeps); err != nil || cmd != nil {
				t.Errorf("expected nil, got %+v, %v", cmd, err)
			}
		})
	}
}

func TestDependentsOf(t *testing.T) {
	staging, prod := uuid.New(), uuid.New()
	envNames := map[uuid.UUID]string{staging: "staging", prod: "prod"}
	api, web, worker := uuid.New(), uuid.New(), uuid.New()
	deployment := func(service uuid.UUID, name string, env uuid.UUID, kind db.DeploymentKind, status db.DeploymentState, imports string) db.Deployment {
		cfg := serviceconfig.ServiceConfig{Name: name}
		if imports != "" {
			cfg.Dependencies = map[string]serviceconfig.DependencyDeclaration{
				"backend": {Type: serviceconfig.DependencyTypeService, Config: map[string]any{"service": imports}},
				"db":      {Type: "postgres"},
			}
		}
		return db.Deployment{ServiceID: service, EnvironmentID: env, Kind: kind, Status: status, ServiceConfigSnapshot: cfg}
	}
	latest := []db.Deployment{
		deployment(api, "api", staging, db.DeploymentKindDeploy, db.DeploymentSucceeded, ""),
		deployment(api, "api", prod, db.DeploymentKindDelete, db.DeploymentSucceeded, ""),
		// web imports api in staging, where api is live, and in prod, where it is not.
		deployment(web, "web", staging, db.DeploymentKindDeploy, db.DeploymentFailed, "api"),
		deployment(web, "web", prod, db.DeploymentKindDeploy, db.DeploymentSucceeded, "api"),
		// worker imported api in staging, but was deleted there.
		deployment(worker, "worker", staging, db.DeploymentKindDelete, db.DeploymentSucceeded, "api"),
	}
	live := liveDeployments(latest, api)
	if len(live) != 1 || live[0].EnvironmentID != staging {
		t.Fatalf("expected api live in staging only, got %+v", live)
	}
	if got := dependentsOf("api", live, latest, envNames); !slices.Equal(got, []string{"web in staging"}) {
		t.Errorf("expected only web in staging, got %q", got)
	}
	if got := dependentsOf("web", liveDeployments(latest, web), latest, envNames); len(got) != 0 {
		t.Errorf("nothing imports web, got %q", got)
	}
}
