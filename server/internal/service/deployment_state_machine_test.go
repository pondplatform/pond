package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
	"gopkg.in/yaml.v3"
)

// machineFixture is a deployment with one managed dependency, "db", and
// whatever commands a test gives it.
type machineFixture struct {
	deployment   db.Deployment
	dependencies []db.DependencyDeployment
	commands     []*db.Command
}

func newMachineFixture(configValues map[string]any) *machineFixture {
	d := planTestDeployment(map[string]string{"db": "postgres"}, configValues)
	d.ServiceID = uuid.New()
	return &machineFixture{
		deployment:   d,
		dependencies: []db.DependencyDeployment{{DependencyName: "db", DependencyType: "postgres", Managed: true}},
	}
}

func (f *machineFixture) withTofu(status db.CommandStatus, output, errMsg string) *machineFixture {
	cmd := &db.Command{ID: uuid.New(), Type: db.CommandTypeTofuApply, Status: status, Output: json.RawMessage(output), Error: errMsg}
	f.dependencies[0].CommandID = &cmd.ID
	f.commands = append(f.commands, cmd)
	return f
}

func (f *machineFixture) withHelm(status db.CommandStatus, errMsg string) *machineFixture {
	f.commands = append(f.commands, &db.Command{ID: uuid.New(), Type: db.CommandTypeHelmUpgrade, Status: status, Error: errMsg})
	return f
}

func (f *machineFixture) state() deploymentState {
	return currentState(f.deployment, f.dependencies, f.commands)
}

func TestCurrentState(t *testing.T) {
	ended := func(state db.DeploymentState) *machineFixture {
		f := newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{}`, "")
		f.deployment.Status = state
		return f
	}
	cases := map[string]struct {
		fixture *machineFixture
		want    deploymentState
	}{
		"succeeded":                         {ended(db.DeploymentSucceeded), stateFinished},
		"cancelled, nothing running":        {ended(db.DeploymentCancelled), stateFinished},
		"cancelled, helm still running":     {ended(db.DeploymentCancelled).withHelm(db.CommandStatusDispatched, ""), stateStopping},
		"failed, sibling still running":     {ended(db.DeploymentFailed).withHelm(db.CommandStatusDispatched, ""), stateStopping},
		"tofu queued":                       {newMachineFixture(nil).withTofu(db.CommandStatusQueued, ``, ""), stateProvisioning},
		"tofu running":                      {newMachineFixture(nil).withTofu(db.CommandStatusDispatched, ``, ""), stateProvisioning},
		"tofu failed":                       {newMachineFixture(nil).withTofu(db.CommandStatusFailed, ``, "boom"), stateDependencyFailed},
		"tofu outputs not an object":        {newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `[1]`, ""), stateDependencyFailed},
		"managed dependency without a tofu": {newMachineFixture(nil), stateDependencyFailed},
		"tofu succeeded":                    {newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{"host":"db"}`, ""), stateDependenciesReady},
		"helm queued":                       {newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{}`, "").withHelm(db.CommandStatusQueued, ""), stateRollingOut},
		"helm running":                      {newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{}`, "").withHelm(db.CommandStatusDispatched, ""), stateRollingOut},
		"helm failed":                       {newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{}`, "").withHelm(db.CommandStatusFailed, "x"), stateRolloutFailed},
		"helm succeeded":                    {newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{}`, "").withHelm(db.CommandStatusSucceeded, ""), stateRolledOut},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.fixture.state(); got != tc.want {
				t.Errorf("expected state %d, got %d", tc.want, got)
			}
		})
	}

	t.Run("no dependencies", func(t *testing.T) {
		d := planTestDeployment(nil, nil)
		if got := currentState(d, nil, nil); got != stateDependenciesReady {
			t.Errorf("expected dependencies ready, got %d", got)
		}
	})
	t.Run("a failure wins over a sibling still provisioning", func(t *testing.T) {
		f := newMachineFixture(nil).withTofu(db.CommandStatusFailed, ``, "boom")
		running := &db.Command{ID: uuid.New(), Type: db.CommandTypeTofuApply, Status: db.CommandStatusDispatched}
		f.dependencies = append(f.dependencies, db.DependencyDeployment{DependencyName: "cache", Managed: true, CommandID: &running.ID})
		f.commands = append(f.commands, running)
		if got := f.state(); got != stateDependencyFailed {
			t.Errorf("expected dependency failed, got %d", got)
		}
	})
}

// writes records what the state machine wrote.
type writes struct {
	saved     *db.Deployment
	created   []*db.Command
	cancelled [][]db.CommandStatus
	running   bool
}

func (f *machineFixture) repos(w *writes) store.Repos {
	return store.Repos{
		Deployments: &testutil.MockDeploymentInfoStore{
			ListDepConfigsFn: func(context.Context, uuid.UUID) ([]db.DependencyDeployment, error) {
				return f.dependencies, nil
			},
			ListCommandsByDeploymentFn: func(context.Context, uuid.UUID) ([]*db.Command, error) {
				return f.commands, nil
			},
			SaveDeploymentFn: func(_ context.Context, d *db.Deployment) error {
				copied := *d
				w.saved = &copied
				return nil
			},
			CreateCommandFn: func(_ context.Context, cmd *db.Command) error {
				w.created = append(w.created, cmd)
				return nil
			},
			UpdateCommandsByDeploymentFn: func(_ context.Context, _ uuid.UUID, _ db.CommandStatus, from ...db.CommandStatus) error {
				w.cancelled = append(w.cancelled, from)
				return nil
			},
		},
		Services: &testutil.MockServiceRepository{
			SetRunningDeploymentFn: func(context.Context, uuid.UUID, uuid.UUID) error {
				w.running = true
				return nil
			},
		},
		Envs: &testutil.MockEnvironmentRepository{
			GetByIDFn: func(context.Context, uuid.UUID) (*db.Environment, error) { return &testEnv, nil },
		},
	}
}

func testMachine() *stateMachine {
	return &stateMachine{helmGen: helmgen.NewGenerator(), now: time.Now}
}

func (f *machineFixture) advance(t *testing.T) *writes {
	t.Helper()
	w := &writes{}
	if err := testMachine().advance(context.Background(), f.repos(w), &f.deployment); err != nil {
		t.Fatalf("advance: %v", err)
	}
	return w
}

func reasonOf(d *db.Deployment) string {
	if d == nil || d.Error == nil {
		return ""
	}
	return *d.Error
}

func TestAdvance_DependenciesReadyStartsTheRollout(t *testing.T) {
	f := newMachineFixture(map[string]any{"DB_HOST": "{{db.host}}"}).withTofu(db.CommandStatusSucceeded, `{"host":"db.internal"}`, "")
	w := f.advance(t)

	if w.saved != nil || len(w.created) != 1 {
		t.Fatalf("expected only a helm command, got saved=%+v created=%+v", w.saved, w.created)
	}
	helm := w.created[0]
	if helm.Type != db.CommandTypeHelmUpgrade || helm.Status != db.CommandStatusQueued || helm.ClusterID != testEnv.ClusterID {
		t.Fatalf("expected a queued helm command for the environment's cluster, got %+v", helm)
	}
	var payload wire.HelmUpgradePayload
	if err := json.Unmarshal(helm.Payload, &payload); err != nil {
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
	if file, _ := base64.StdEncoding.DecodeString(values.Configs[0].Data); !strings.Contains(string(file), "db.internal") {
		t.Errorf("expected the dependency's output in the rendered config, got %q", file)
	}
}

func TestAdvance_TheRolloutRendersTheCommand(t *testing.T) {
	f := newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{"host":"db.internal","port":5432}`, "")
	f.deployment.ServiceConfigSnapshot.Command = []string{"serve", "--db={{db.host}}:{{db.port}}"}
	w := f.advance(t)

	if len(w.created) != 1 {
		t.Fatalf("expected a helm command, got saved=%+v created=%+v", w.saved, w.created)
	}
	var payload wire.HelmUpgradePayload
	if err := json.Unmarshal(w.created[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var values struct {
		Args []string `yaml:"args"`
	}
	if err := yaml.Unmarshal(payload.Values, &values); err != nil {
		t.Fatalf("decode helm values %s: %v", payload.Values, err)
	}
	if want := []string{"serve", "--db=db.internal:5432"}; !slices.Equal(values.Args, want) {
		t.Errorf("expected args %q, got %q", want, values.Args)
	}
}

func TestAdvance_AnUnrenderableCommandFailsTheDeployment(t *testing.T) {
	f := newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{"host":"db.internal"}`, "")
	f.deployment.ServiceConfigSnapshot.Command = []string{"serve", "--user={{db.username}}"}
	w := f.advance(t)
	if w.saved == nil || w.saved.Status != db.DeploymentFailed || !strings.Contains(reasonOf(w.saved), "command[1]") || len(w.created) != 0 {
		t.Errorf("expected failed naming command[1] and no command, got saved=%+v created=%+v", w.saved, w.created)
	}
}

func TestAdvance_AnUnrenderableRolloutFailsTheDeployment(t *testing.T) {
	f := newMachineFixture(map[string]any{"DB_USER": "{{db.username}}"}).withTofu(db.CommandStatusSucceeded, `{"host":"db.internal"}`, "")
	w := f.advance(t)
	if w.saved == nil || w.saved.Status != db.DeploymentFailed || !strings.Contains(reasonOf(w.saved), "db.username") || len(w.created) != 0 {
		t.Errorf("expected failed naming db.username and no command, got saved=%+v created=%+v", w.saved, w.created)
	}
}

func TestAdvance_Failures(t *testing.T) {
	cases := map[string]struct {
		fixture *machineFixture
		reason  string
	}{
		"dependency": {newMachineFixture(nil).withTofu(db.CommandStatusFailed, ``, "exit status 1"),
			`dependency "db" failed to provision: exit status 1`},
		"tofu outputs": {newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `[1]`, ""),
			`dependency "db": tofu outputs must be a JSON object, got [1]`},
		"rollout": {newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{}`, "").withHelm(db.CommandStatusFailed, "timed out"),
			"helm upgrade failed: timed out"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := tc.fixture.advance(t)
			if w.saved == nil || w.saved.Status != db.DeploymentFailed || reasonOf(w.saved) != tc.reason || w.saved.CompletedAt == nil {
				t.Fatalf("expected failed with %q, got %+v", tc.reason, w.saved)
			}
			if len(w.cancelled) != 1 || len(w.cancelled[0]) != 1 || w.cancelled[0][0] != db.CommandStatusQueued {
				t.Errorf("expected only queued commands cancelled, got %v", w.cancelled)
			}
		})
	}
}

func TestAdvance_RolledOutCompletesTheDeployment(t *testing.T) {
	w := newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{}`, "").withHelm(db.CommandStatusSucceeded, "").advance(t)
	if w.saved == nil || w.saved.Status != db.DeploymentSucceeded || !w.running {
		t.Errorf("expected succeeded and the running deployment set, got saved=%+v running=%v", w.saved, w.running)
	}
}

func TestAdvance_WaitingStatesWriteNothing(t *testing.T) {
	cancelled := newMachineFixture(nil).withTofu(db.CommandStatusDispatched, ``, "")
	cancelled.deployment.Status = db.DeploymentCancelled
	for name, f := range map[string]*machineFixture{
		"provisioning": newMachineFixture(nil).withTofu(db.CommandStatusDispatched, ``, ""),
		"rolling out":  newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{}`, "").withHelm(db.CommandStatusDispatched, ""),
		"stopping":     cancelled,
	} {
		t.Run(name, func(t *testing.T) {
			if w := f.advance(t); w.saved != nil || len(w.created) != 0 || len(w.cancelled) != 0 {
				t.Errorf("expected no writes, got %+v", w)
			}
		})
	}
}

func TestCancel_StopsADeploymentInProgress(t *testing.T) {
	f := newMachineFixture(nil).withTofu(db.CommandStatusDispatched, ``, "")
	w := &writes{}
	if err := testMachine().cancel(context.Background(), f.repos(w), &f.deployment); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if w.saved == nil || w.saved.Status != db.DeploymentCancelled || w.saved.CompletedAt == nil {
		t.Fatalf("expected cancelled with completedAt, got %+v", w.saved)
	}
	if len(w.cancelled) != 1 || len(w.cancelled[0]) != 1 || w.cancelled[0][0] != db.CommandStatusQueued {
		t.Errorf("expected queued commands cancelled and the running one left to its agent, got %v", w.cancelled)
	}
}

func TestCancel_ConflictsOnceTheDeploymentHasEnded(t *testing.T) {
	stopping := newMachineFixture(nil).withTofu(db.CommandStatusDispatched, ``, "")
	stopping.deployment.Status = db.DeploymentCancelled
	finished := newMachineFixture(nil).withTofu(db.CommandStatusSucceeded, `{}`, "")
	finished.deployment.Status = db.DeploymentSucceeded
	for name, f := range map[string]*machineFixture{"stopping": stopping, "finished": finished} {
		t.Run(name, func(t *testing.T) {
			w := &writes{}
			err := testMachine().cancel(context.Background(), f.repos(w), &f.deployment)
			if !errors.Is(err, api.ErrConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
			if w.saved != nil || len(w.cancelled) != 0 {
				t.Errorf("expected no writes, got %+v", w)
			}
		})
	}
}
