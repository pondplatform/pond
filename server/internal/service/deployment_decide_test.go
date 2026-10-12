package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

var (
	decideNow = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	testEnv   = db.Environment{ID: uuid.New(), ClusterID: uuid.New(), Name: "staging", Namespace: "staging"}
)

// newState builds a pending deployment that declares each dependency as a
// postgres one.
func newState(dependencies ...db.DependencyDeployment) deploymentState {
	deployment := db.Deployment{
		ID:            uuid.New(),
		ServiceID:     uuid.New(),
		EnvironmentID: testEnv.ID,
		ImageTag:      "v1",
		Status:        api.DeploymentStatusPending,
		ServiceConfigSnapshot: serviceconfig.ServiceConfig{
			Name:         "svc",
			Image:        "ghcr.io/example/svc",
			Dependencies: map[string]serviceconfig.DependencyDeclaration{},
		},
	}
	for i := range dependencies {
		dependencies[i].DeploymentId = deployment.ID
		deployment.ServiceConfigSnapshot.Dependencies[dependencies[i].DependencyName] = serviceconfig.DependencyDeclaration{Type: "postgres"}
	}
	return deploymentState{
		Deployment:   deployment,
		Environment:  testEnv,
		Dependencies: dependencies,
		Commands:     map[uuid.UUID]*db.Command{},
	}
}

func dependencyRow(name string, status db.DependencyDeploymentStatus, managed *bool) db.DependencyDeployment {
	return db.DependencyDeployment{ID: uuid.New(), DependencyName: name, Status: status, Managed: managed}
}

func pendingManaged(name string) db.DependencyDeployment {
	return dependencyRow(name, db.DependencyDeploymentStatusPending, ptr(true))
}

// withCommand gives the dependency a command in the given state.
func (s *deploymentState) withCommand(dependency string, status db.CommandStatus, output string) *db.Command {
	cmd := &db.Command{ID: uuid.New(), Type: db.CommandTypeTofuApply, Status: status, Output: json.RawMessage(output)}
	s.Commands[cmd.ID] = cmd
	for i := range s.Dependencies {
		if s.Dependencies[i].DependencyName == dependency {
			s.Dependencies[i].CommandID = &cmd.ID
		}
	}
	return cmd
}

func (s *deploymentState) withHelm(status db.CommandStatus, errMsg string) {
	cmd := &db.Command{ID: uuid.New(), Type: db.CommandTypeHelmUpgrade, Status: status, Error: errMsg}
	s.Commands[cmd.ID] = cmd
	s.Deployment.HelmCommandID = &cmd.ID
}

func decideTest(s deploymentState) decision {
	return decide(s, helmgen.NewGenerator(), decideNow)
}

// apply returns the state persisting d would leave behind.
func apply(s deploymentState, d decision) deploymentState {
	next := deploymentState{
		Deployment:   d.Deployment,
		Environment:  s.Environment,
		Dependencies: append([]db.DependencyDeployment(nil), s.Dependencies...),
		Commands:     map[uuid.UUID]*db.Command{},
	}
	for id, cmd := range s.Commands {
		copied := *cmd
		if d.CancelLive && (cmd.Status == db.CommandStatusQueued || cmd.Status == db.CommandStatusDispatched) {
			copied.Status = db.CommandStatusCancelled
		}
		next.Commands[id] = &copied
	}
	for _, cmd := range d.Commands {
		next.Commands[cmd.ID] = cmd
	}
	for _, changed := range d.Dependencies {
		for i := range next.Dependencies {
			if next.Dependencies[i].DependencyName == changed.DependencyName {
				next.Dependencies[i] = changed
			}
		}
	}
	return next
}

func commandsOfType(d decision, commandType string) int {
	n := 0
	for _, cmd := range d.Commands {
		if cmd.Type == commandType {
			n++
		}
	}
	return n
}

func reason(d decision) string {
	if d.Deployment.Error == nil {
		return ""
	}
	return *d.Deployment.Error
}

func TestDecide_NoDependenciesStartsHelm(t *testing.T) {
	d := decideTest(newState())
	if commandsOfType(d, db.CommandTypeHelmUpgrade) != 1 || len(d.Commands) != 1 {
		t.Fatalf("expected one helm command, got %+v", d.Commands)
	}
	if d.Deployment.HelmCommandID == nil || *d.Deployment.HelmCommandID != d.Commands[0].ID || !d.DeploymentChanged {
		t.Errorf("expected the deployment to record its helm command, got %+v", d.Deployment)
	}
	if cmd := d.Commands[0]; cmd.ClusterID != testEnv.ClusterID || cmd.Status != db.CommandStatusQueued {
		t.Errorf("expected a queued command for the environment's cluster, got %+v", cmd)
	}
}

// H3: unmanaged dependencies complete on the spot, so helm starts in the
// same pass.
func TestDecide_UnmanagedOnlyStartsHelmInOnePass(t *testing.T) {
	cache := dependencyRow("cache", db.DependencyDeploymentStatusPending, ptr(false))
	cache.UserConfig = map[string]any{"url": "http://cache"}
	d := decideTest(newState(cache))

	if len(d.Dependencies) != 1 || d.Dependencies[0].Status != db.DependencyDeploymentStatusSucceeded {
		t.Fatalf("expected cache succeeded, got %+v", d.Dependencies)
	}
	if got := string(d.Dependencies[0].Output); got != `{"url":"http://cache"}` {
		t.Errorf("expected the user config as output, got %s", got)
	}
	if d.Dependencies[0].CompletedAt == nil {
		t.Error("expected completedAt to be set")
	}
	if commandsOfType(d, db.CommandTypeHelmUpgrade) != 1 {
		t.Errorf("expected helm to start, got %+v", d.Commands)
	}
}

// H2: every managed dependency is provisioned once; a second pass while the
// commands run schedules nothing.
func TestDecide_ManagedDependenciesAreProvisionedOnce(t *testing.T) {
	s := newState(pendingManaged("db1"), pendingManaged("db2"))
	d := decideTest(s)
	if commandsOfType(d, db.CommandTypeTofuApply) != 2 || len(d.Commands) != 2 {
		t.Fatalf("expected two tofu commands, got %+v", d.Commands)
	}
	for _, dependency := range d.Dependencies {
		if dependency.CommandID == nil {
			t.Errorf("expected %s linked to its command", dependency.DependencyName)
		}
	}

	again := decideTest(apply(s, d))
	if len(again.Commands) != 0 || len(again.Dependencies) != 0 || again.DeploymentChanged {
		t.Errorf("expected nothing to change while the commands run, got %+v", again)
	}
}

func TestDecide_SucceededTofuRecordsOutputsThenStartsHelm(t *testing.T) {
	s := newState(pendingManaged("db"))
	s.withCommand("db", db.CommandStatusSucceeded, `{"host":"db.internal"}`)
	d := decideTest(s)
	if len(d.Dependencies) != 1 || d.Dependencies[0].Status != db.DependencyDeploymentStatusSucceeded || string(d.Dependencies[0].Output) != `{"host":"db.internal"}` {
		t.Fatalf("expected db succeeded with its outputs, got %+v", d.Dependencies)
	}
	if commandsOfType(d, db.CommandTypeHelmUpgrade) != 1 {
		t.Errorf("expected helm to start, got %+v", d.Commands)
	}
}

func TestDecide_WaitsForAPendingSibling(t *testing.T) {
	s := newState(pendingManaged("db1"), pendingManaged("db2"))
	s.withCommand("db1", db.CommandStatusSucceeded, `{}`)
	s.withCommand("db2", db.CommandStatusDispatched, ``)
	d := decideTest(s)
	if len(d.Commands) != 0 || d.Deployment.Status != api.DeploymentStatusPending {
		t.Errorf("expected to wait for db2, got %+v", d)
	}
}

func TestDecide_Failures(t *testing.T) {
	cases := map[string]struct {
		state    func() deploymentState
		inReason string
	}{
		"dependency command failed": {
			state: func() deploymentState {
				s := newState(pendingManaged("db"), pendingManaged("other"))
				s.withCommand("db", db.CommandStatusFailed, ``).Error = "tofu apply: exit status 1"
				s.withCommand("other", db.CommandStatusQueued, ``)
				return s
			},
			inReason: `dependency "db" failed to provision: tofu apply: exit status 1`,
		},
		"tofu outputs not an object": {
			state: func() deploymentState {
				s := newState(pendingManaged("db"))
				s.withCommand("db", db.CommandStatusSucceeded, `[1]`)
				return s
			},
			inReason: `dependency "db": tofu outputs must be a JSON object, got [1]`,
		},
		"template does not render": {
			state: func() deploymentState {
				s := newState(pendingManaged("db"))
				s.Deployment.ServiceConfigSnapshot.Configs = map[string]serviceconfig.ConfigFileSpec{
					"app.env": {Format: ".env", MountDir: "/etc/app", Values: map[string]any{"USER": "{{db.username}}"}},
				}
				s.withCommand("db", db.CommandStatusSucceeded, `{"host":"db.internal"}`)
				return s
			},
			inReason: "db.username",
		},
		"helm failed": {
			state: func() deploymentState {
				s := newState()
				s.withHelm(db.CommandStatusFailed, "timed out waiting for the condition")
				return s
			},
			inReason: "helm upgrade failed: timed out waiting for the condition",
		},
		"pending without a managed flag": {
			state: func() deploymentState {
				return newState(dependencyRow("db", db.DependencyDeploymentStatusPending, nil))
			},
			inReason: "no managed flag",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := tc.state()
			d := decideTest(s)
			if d.Deployment.Status != api.DeploymentStatusFailed || d.Deployment.CompletedAt == nil {
				t.Fatalf("expected failed with completedAt, got %+v", d.Deployment)
			}
			if !strings.Contains(reason(d), tc.inReason) {
				t.Errorf("expected the reason to contain %q, got %q", tc.inReason, reason(d))
			}
			if len(d.Commands) != 0 || !d.CancelLive {
				t.Errorf("expected no new commands and live ones cancelled, got %+v", d)
			}
			if again := decideTest(apply(s, d)); again.DeploymentChanged || len(again.Commands) != 0 {
				t.Errorf("a failed deployment must stay as it is, got %+v", again)
			}
		})
	}
}

func TestDecide_HelmSucceededFinishesTheDeployment(t *testing.T) {
	s := newState()
	s.withHelm(db.CommandStatusSucceeded, "")
	d := decideTest(s)
	if d.Deployment.Status != api.DeploymentStatusSucceeded || d.Deployment.CompletedAt == nil || !d.SetServiceRunning {
		t.Errorf("expected succeeded and the service's running deployment set, got %+v", d)
	}
}

func TestDecide_WaitsForInput(t *testing.T) {
	d := decideTest(newState(dependencyRow("db", db.DependencyDeploymentStatusAwaitingInput, nil), pendingManaged("other")))
	if d.Deployment.Status != api.DeploymentStatusAwaitingInput || len(d.Commands) != 0 {
		t.Errorf("expected awaiting_input with nothing scheduled, got %+v", d)
	}
}

// H13: a result that arrives after a cancel changes nothing.
func TestDecide_FinishedDeploymentIgnoresLateResults(t *testing.T) {
	for _, status := range []db.DeploymentStatus{api.DeploymentStatusCancelled, api.DeploymentStatusSucceeded, api.DeploymentStatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			s := newState(pendingManaged("db"))
			s.Deployment.Status = status
			s.withCommand("db", db.CommandStatusSucceeded, `{"host":"db"}`)
			d := decideTest(s)
			if d.DeploymentChanged || len(d.Commands) != 0 || len(d.Dependencies) != 0 || d.Deployment.Status != status {
				t.Errorf("expected nothing to change, got %+v", d)
			}
		})
	}
}

func TestDecide_ReprovisionsADependencyWhoseCommandIsGone(t *testing.T) {
	s := newState(pendingManaged("db"))
	missing := uuid.New()
	s.Dependencies[0].CommandID = &missing
	d := decideTest(s)
	if commandsOfType(d, db.CommandTypeTofuApply) != 1 {
		t.Errorf("expected a new tofu command, got %+v", d.Commands)
	}
}
