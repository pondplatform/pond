package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

var testClusterID = uuid.New()

func makeStateMachine(store *testutil.MockDeploymentInfoStore, dependencies *mockDependencyService, topics *testutil.MockTopics) *stateMachine {
	if dependencies == nil {
		dependencies = &mockDependencyService{}
	}
	if topics == nil {
		topics = &testutil.MockTopics{}
	}
	envs := &testutil.MockEnvironmentRepository{
		GetByIDFn: func(context.Context, uuid.UUID) (*db.Environment, error) {
			return &db.Environment{ID: uuid.New(), ClusterID: testClusterID, Namespace: "default"}, nil
		},
	}
	return &stateMachine{
		deploymentInfo: store,
		envs:           envs,
		dependencies:   dependencies,
		helmGen:        &testutil.MockHelmValuesGenerator{},
		tmplRenderer:   NewTemplateRenderer(),
		tx:             inlineTx(store),
		topics:         topics,
		log:            slog.Default(),
	}
}

func testDeployment() db.Deployment {
	return db.Deployment{ID: uuid.New(), ServiceConfigSnapshot: serviceconfig.ServiceConfig{Name: "my-svc"}}
}

func noDependencyScheduling(t *testing.T) *mockDependencyService {
	return &mockDependencyService{
		scheduleCommandFn: func(context.Context, db.Deployment, db.DependencyDeployment) error {
			t.Error("no dependency should be scheduled")
			return nil
		},
	}
}

// helmRecorder stubs the store calls that starting helm makes and counts the
// helm commands created.
func helmRecorder(store *testutil.MockDeploymentInfoStore) *int {
	created := 0
	store.GetDepOutputsByDeploymentFn = func(context.Context, uuid.UUID) (map[string]json.RawMessage, error) { return nil, nil }
	store.CreateCommandFn = func(_ context.Context, cmd *db.Command) error {
		if cmd.Type == db.CommandTypeHelmUpgrade {
			created++
		}
		return nil
	}
	store.SetHelmCommandIDFn = func(context.Context, uuid.UUID, uuid.UUID) error { return nil }
	return &created
}

// --- InitializeDeployment ---

func TestInitializeDeployment_AwaitingInputSetsStatus(t *testing.T) {
	var status db.DeploymentStatus
	store := &testutil.MockDeploymentInfoStore{
		UpdateStatusFn: func(_ context.Context, _ uuid.UUID, s db.DeploymentStatus, _ *time.Time) error {
			status = s
			return nil
		},
	}
	deps := []db.DependencyDeployment{
		{DependencyName: "cache", Status: db.DependencyDeploymentStatusPending, Managed: ptr(true)},
		{DependencyName: "db", Status: db.DependencyDeploymentStatusAwaitingInput},
	}

	m := makeStateMachine(store, noDependencyScheduling(t), nil)
	if err := m.InitializeDeployment(context.Background(), testDeployment(), deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != api.DeploymentStatusAwaitingInput {
		t.Errorf("expected awaiting_input, got %s", status)
	}
}

func TestInitializeDeployment_FailedDependencyFailsDeployment(t *testing.T) {
	var reason string
	store := &testutil.MockDeploymentInfoStore{
		SetFailedFn: func(_ context.Context, _ uuid.UUID, msg string, _ *time.Time) error {
			reason = msg
			return nil
		},
	}
	deps := []db.DependencyDeployment{{DependencyName: "db", Status: db.DependencyDeploymentStatusFailed}}

	m := makeStateMachine(store, noDependencyScheduling(t), nil)
	if err := m.InitializeDeployment(context.Background(), testDeployment(), deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reason != dependencyFailureReason {
		t.Errorf("expected %q, got %q", dependencyFailureReason, reason)
	}
}

func TestInitializeDeployment_NoDependenciesStartsHelm(t *testing.T) {
	var status db.DeploymentStatus
	store := &testutil.MockDeploymentInfoStore{
		UpdateStatusFn: func(_ context.Context, _ uuid.UUID, s db.DeploymentStatus, _ *time.Time) error {
			status = s
			return nil
		},
	}
	helmCreated := helmRecorder(store)
	topics := &testutil.MockTopics{}

	m := makeStateMachine(store, nil, topics)
	if err := m.InitializeDeployment(context.Background(), testDeployment(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != api.DeploymentStatusPending {
		t.Errorf("expected pending, got %s", status)
	}
	if *helmCreated != 1 {
		t.Errorf("expected 1 helm command, got %d", *helmCreated)
	}
	if n := len(topics.CommandQueued.For(testClusterID).Published()); n != 1 {
		t.Errorf("expected 1 CommandQueued, got %d", n)
	}
}

func TestInitializeDeployment_UnmanagedOnlyStartsHelm(t *testing.T) {
	unmanaged := db.DependencyDeployment{DependencyName: "cache", Status: db.DependencyDeploymentStatusPending, Managed: ptr(false)}
	store := &testutil.MockDeploymentInfoStore{
		ListDepConfigsFn: func(context.Context, uuid.UUID) ([]db.DependencyDeployment, error) {
			done := unmanaged
			done.Status = db.DependencyDeploymentStatusSucceeded
			return []db.DependencyDeployment{done}, nil
		},
	}
	helmCreated := helmRecorder(store)
	var scheduled []string
	deps := &mockDependencyService{
		scheduleCommandFn: func(_ context.Context, _ db.Deployment, d db.DependencyDeployment) error {
			scheduled = append(scheduled, d.DependencyName)
			return nil
		},
	}

	m := makeStateMachine(store, deps, nil)
	if err := m.InitializeDeployment(context.Background(), testDeployment(), []db.DependencyDeployment{unmanaged}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(scheduled, []string{"cache"}) {
		t.Errorf("expected cache scheduled, got %v", scheduled)
	}
	if *helmCreated != 1 {
		t.Errorf("expected helm to start once the unmanaged dependency is done, got %d helm commands", *helmCreated)
	}
}

func TestInitializeDeployment_ManagedLeavesAdvancingToTheResult(t *testing.T) {
	var calls []string
	store := &testutil.MockDeploymentInfoStore{
		UpdateStatusFn: func(context.Context, uuid.UUID, db.DeploymentStatus, *time.Time) error {
			calls = append(calls, "set pending")
			return nil
		},
		ListDepConfigsFn: func(context.Context, uuid.UUID) ([]db.DependencyDeployment, error) {
			t.Error("the deployment must not advance while a tofu.apply is queued")
			return nil, nil
		},
	}
	deps := &mockDependencyService{
		scheduleCommandFn: func(_ context.Context, _ db.Deployment, d db.DependencyDeployment) error {
			calls = append(calls, "schedule "+d.DependencyName)
			return nil
		},
	}
	input := []db.DependencyDeployment{
		{DependencyName: "db", Status: db.DependencyDeploymentStatusPending, Managed: ptr(true)},
		{DependencyName: "cache", Status: db.DependencyDeploymentStatusPending, Managed: ptr(false)},
	}

	m := makeStateMachine(store, deps, nil)
	if err := m.InitializeDeployment(context.Background(), testDeployment(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []string{"set pending", "schedule cache", "schedule db"}; !slices.Equal(calls, want) {
		t.Errorf("expected %v, got %v", want, calls)
	}
}

func TestInitializeDeployment_SkipsDependencyWithLiveCommand(t *testing.T) {
	cmdID := uuid.New()
	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn: func(_ context.Context, id uuid.UUID) (*db.Command, error) {
			return &db.Command{ID: id, Status: db.CommandStatusDispatched}, nil
		},
		ListDepConfigsFn: func(context.Context, uuid.UUID) ([]db.DependencyDeployment, error) {
			return []db.DependencyDeployment{{Status: db.DependencyDeploymentStatusPending}}, nil
		},
		CreateCommandFn: func(context.Context, *db.Command) error {
			t.Error("no command should be created while a tofu.apply runs")
			return nil
		},
	}
	input := []db.DependencyDeployment{{DependencyName: "db", Status: db.DependencyDeploymentStatusPending, Managed: ptr(true), CommandID: &cmdID}}

	m := makeStateMachine(store, noDependencyScheduling(t), nil)
	if err := m.InitializeDeployment(context.Background(), testDeployment(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitializeDeployment_ScheduleErrorPropagates(t *testing.T) {
	scheduleErr := errors.New("insert failed")
	deps := &mockDependencyService{
		scheduleCommandFn: func(context.Context, db.Deployment, db.DependencyDeployment) error { return scheduleErr },
	}
	input := []db.DependencyDeployment{{DependencyName: "db", Status: db.DependencyDeploymentStatusPending, Managed: ptr(true)}}

	m := makeStateMachine(&testutil.MockDeploymentInfoStore{}, deps, nil)
	if err := m.InitializeDeployment(context.Background(), testDeployment(), input); !errors.Is(err, scheduleErr) {
		t.Fatalf("expected schedule error, got %v", err)
	}
}

// --- dependenciesToSchedule ---

func TestDependenciesToSchedule(t *testing.T) {
	commandWith := func(status db.CommandStatus) *db.Command { return &db.Command{ID: uuid.New(), Status: status} }
	queued, dispatched := commandWith(db.CommandStatusQueued), commandWith(db.CommandStatusDispatched)
	succeeded, failed, cancelled := commandWith(db.CommandStatusSucceeded), commandWith(db.CommandStatusFailed), commandWith(db.CommandStatusCancelled)
	commands := map[uuid.UUID]*db.Command{}
	for _, c := range []*db.Command{queued, dispatched, succeeded, failed, cancelled} {
		commands[c.ID] = c
	}
	pending := func(name string, managed bool, cmd *db.Command) db.DependencyDeployment {
		d := db.DependencyDeployment{DependencyName: name, Status: db.DependencyDeploymentStatusPending, Managed: ptr(managed)}
		if cmd != nil {
			d.CommandID = &cmd.ID
		}
		return d
	}

	deps := []db.DependencyDeployment{
		pending("no-command", true, nil),
		pending("queued", true, queued),
		pending("dispatched", true, dispatched),
		pending("succeeded", true, succeeded),
		pending("failed", true, failed),
		pending("cancelled", true, cancelled),
		pending("unmanaged", false, nil),
		pending("missing-command", true, &db.Command{ID: uuid.New()}),
		{DependencyName: "done", Status: db.DependencyDeploymentStatusSucceeded, Managed: ptr(true)},
		{DependencyName: "awaiting", Status: db.DependencyDeploymentStatusAwaitingInput},
	}

	var got []string
	for _, d := range dependenciesToSchedule(deps, commands) {
		got = append(got, d.DependencyName)
	}
	want := []string{"unmanaged", "no-command", "succeeded", "failed", "cancelled", "missing-command"}
	if !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

// --- AdvanceOnCommandResult ---

func TestAdvanceOnCommandResult_RecordsSuccess(t *testing.T) {
	cmd := &db.Command{ID: uuid.New(), DeploymentID: uuid.New(), Type: db.CommandTypeHelmUpgrade}
	var updated *db.Command
	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn: func(context.Context, uuid.UUID) (*db.Command, error) { return cmd, nil },
		UpdateCommandFn: func(_ context.Context, c *db.Command) error {
			updated = c
			return nil
		},
		GetByIDFn: func(_ context.Context, id uuid.UUID) (*db.Deployment, error) { return &db.Deployment{ID: id}, nil },
	}

	m := makeStateMachine(store, nil, nil)
	output := json.RawMessage(`{"ok":true}`)
	if err := m.AdvanceOnCommandResult(context.Background(), events.CommandResult{CommandID: cmd.ID, Success: true, Output: output}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated == nil || updated.Status != db.CommandStatusSucceeded || string(updated.Output) != string(output) {
		t.Errorf("expected command recorded as succeeded with output, got %+v", updated)
	}
}

func TestAdvanceOnCommandResult_RecordsFailure(t *testing.T) {
	cmd := &db.Command{ID: uuid.New(), DeploymentID: uuid.New(), Type: db.CommandTypeHelmUpgrade}
	var updated *db.Command
	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn: func(context.Context, uuid.UUID) (*db.Command, error) { return cmd, nil },
		UpdateCommandFn: func(_ context.Context, c *db.Command) error {
			updated = c
			return nil
		},
		GetByIDFn: func(_ context.Context, id uuid.UUID) (*db.Deployment, error) { return &db.Deployment{ID: id}, nil },
	}

	m := makeStateMachine(store, nil, nil)
	if err := m.AdvanceOnCommandResult(context.Background(), events.CommandResult{CommandID: cmd.ID, Error: "boom"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated == nil || updated.Status != db.CommandStatusFailed || updated.Error != "boom" {
		t.Errorf("expected command recorded as failed with its error, got %+v", updated)
	}
}

func TestAdvanceOnCommandResult_GetCommandErrorPropagates(t *testing.T) {
	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn: func(context.Context, uuid.UUID) (*db.Command, error) { return nil, errors.New("db error") },
	}
	m := makeStateMachine(store, nil, nil)
	if err := m.AdvanceOnCommandResult(context.Background(), events.CommandResult{CommandID: uuid.New()}); err == nil {
		t.Fatal("expected error when GetCommand fails")
	}
}

// tofuResultStore stubs a finished tofu.apply for dependency "db" of a
// deployment whose dependencies are then listed as deps.
func tofuResultStore(cmd *db.Command, deps []db.DependencyDeployment) *testutil.MockDeploymentInfoStore {
	return &testutil.MockDeploymentInfoStore{
		GetCommandFn:    func(context.Context, uuid.UUID) (*db.Command, error) { return cmd, nil },
		UpdateCommandFn: func(context.Context, *db.Command) error { return nil },
		GetDepConfigByCommandIDFn: func(context.Context, uuid.UUID) (uuid.UUID, *db.DependencyDeployment, error) {
			return cmd.DeploymentID, &db.DependencyDeployment{DependencyName: "db"}, nil
		},
		GetByIDFn: func(_ context.Context, id uuid.UUID) (*db.Deployment, error) {
			return &db.Deployment{ID: id, ServiceConfigSnapshot: serviceconfig.ServiceConfig{Name: "svc"}}, nil
		},
		ListDepConfigsFn: func(context.Context, uuid.UUID) ([]db.DependencyDeployment, error) { return deps, nil },
	}
}

func TestAdvanceOnCommandResult_TofuSuccessMarksDependencyAndStartsHelm(t *testing.T) {
	cmd := &db.Command{ID: uuid.New(), DeploymentID: uuid.New(), Type: db.CommandTypeTofuApply}
	store := tofuResultStore(cmd, []db.DependencyDeployment{{DependencyName: "db", Status: db.DependencyDeploymentStatusSucceeded}})
	var markedOutput json.RawMessage
	store.MarkDepConfigSucceededFn = func(_ context.Context, id uuid.UUID, name string, output json.RawMessage) error {
		if id != cmd.DeploymentID || name != "db" {
			t.Errorf("expected db of %v, got %s of %v", cmd.DeploymentID, name, id)
		}
		markedOutput = output
		return nil
	}
	helmCreated := helmRecorder(store)

	m := makeStateMachine(store, noDependencyScheduling(t), nil)
	output := json.RawMessage(`{"host":"db.internal"}`)
	if err := m.AdvanceOnCommandResult(context.Background(), events.CommandResult{CommandID: cmd.ID, Success: true, Output: output}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(markedOutput) != string(output) {
		t.Errorf("expected the tofu output stored on the dependency, got %s", markedOutput)
	}
	if *helmCreated != 1 {
		t.Errorf("expected 1 helm command, got %d", *helmCreated)
	}
}

func TestAdvanceOnCommandResult_TofuSuccessWithPendingSiblingWaits(t *testing.T) {
	cmd := &db.Command{ID: uuid.New(), DeploymentID: uuid.New(), Type: db.CommandTypeTofuApply}
	store := tofuResultStore(cmd, []db.DependencyDeployment{
		{DependencyName: "db", Status: db.DependencyDeploymentStatusSucceeded},
		{DependencyName: "cache", Status: db.DependencyDeploymentStatusPending, Managed: ptr(true)},
	})
	store.MarkDepConfigSucceededFn = func(context.Context, uuid.UUID, string, json.RawMessage) error { return nil }
	store.CreateCommandFn = func(context.Context, *db.Command) error {
		t.Error("no command should be created while a sibling is still provisioning")
		return nil
	}
	topics := &testutil.MockTopics{}

	m := makeStateMachine(store, noDependencyScheduling(t), topics)
	if err := m.AdvanceOnCommandResult(context.Background(), events.CommandResult{CommandID: cmd.ID, Success: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := topics.CommandQueued.PublishedCount(); n != 0 {
		t.Errorf("expected no wake, got %d", n)
	}
}

func TestAdvanceOnCommandResult_TofuFailureFailsDependencyAndDeployment(t *testing.T) {
	cmd := &db.Command{ID: uuid.New(), DeploymentID: uuid.New(), Type: db.CommandTypeTofuApply}
	store := tofuResultStore(cmd, []db.DependencyDeployment{{DependencyName: "db", Status: db.DependencyDeploymentStatusFailed}})
	var failedDep, reason string
	var cancelledFrom, cancelledTo db.CommandStatus
	store.MarkDepConfigFailedFn = func(_ context.Context, _ uuid.UUID, name string) error {
		failedDep = name
		return nil
	}
	store.UpdateCommandsByDeploymentFn = func(_ context.Context, _ uuid.UUID, from, to db.CommandStatus) error {
		cancelledFrom, cancelledTo = from, to
		return nil
	}
	store.SetFailedFn = func(_ context.Context, _ uuid.UUID, msg string, _ *time.Time) error {
		reason = msg
		return nil
	}

	m := makeStateMachine(store, nil, nil)
	if err := m.AdvanceOnCommandResult(context.Background(), events.CommandResult{CommandID: cmd.ID, Error: "tofu failed"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if failedDep != "db" {
		t.Errorf("expected db marked failed, got %q", failedDep)
	}
	if cancelledFrom != db.CommandStatusQueued || cancelledTo != db.CommandStatusCancelled {
		t.Errorf("expected queued siblings cancelled, got %s→%s", cancelledFrom, cancelledTo)
	}
	if reason != dependencyFailureReason {
		t.Errorf("expected deployment failed with %q, got %q", dependencyFailureReason, reason)
	}
}

func TestAdvanceOnCommandResult_TofuWithoutDependencyIsIgnored(t *testing.T) {
	cmd := &db.Command{ID: uuid.New(), DeploymentID: uuid.New(), Type: db.CommandTypeTofuApply}
	store := tofuResultStore(cmd, nil)
	store.GetDepConfigByCommandIDFn = func(context.Context, uuid.UUID) (uuid.UUID, *db.DependencyDeployment, error) {
		return uuid.Nil, nil, nil
	}
	store.GetByIDFn = func(context.Context, uuid.UUID) (*db.Deployment, error) {
		t.Error("a command without a dependency must not advance any deployment")
		return nil, nil
	}

	m := makeStateMachine(store, nil, nil)
	if err := m.AdvanceOnCommandResult(context.Background(), events.CommandResult{CommandID: cmd.ID, Success: true}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestAdvanceOnCommandResult_UnknownCommandTypeIsIgnored(t *testing.T) {
	cmd := &db.Command{ID: uuid.New(), Type: "mystery"}
	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn:    func(context.Context, uuid.UUID) (*db.Command, error) { return cmd, nil },
		UpdateCommandFn: func(context.Context, *db.Command) error { return nil },
	}
	m := makeStateMachine(store, nil, nil)
	if err := m.AdvanceOnCommandResult(context.Background(), events.CommandResult{CommandID: cmd.ID, Success: true}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

// --- advanceOnHelmResult ---

func TestAdvanceOnHelmResult_SuccessTransitionsToSucceeded(t *testing.T) {
	var status db.DeploymentStatus
	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, id uuid.UUID) (*db.Deployment, error) { return &db.Deployment{ID: id}, nil },
		UpdateStatusFn: func(_ context.Context, _ uuid.UUID, s db.DeploymentStatus, completedAt *time.Time) error {
			status = s
			if completedAt == nil {
				t.Error("expected completedAt to be set")
			}
			return nil
		},
	}

	m := makeStateMachine(store, nil, nil)
	if err := m.advanceOnHelmResult(context.Background(), &db.Command{ID: uuid.New(), DeploymentID: uuid.New(), Status: db.CommandStatusSucceeded}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != api.DeploymentStatusSucceeded {
		t.Errorf("expected succeeded, got %s", status)
	}
}

func TestAdvanceOnHelmResult_FailureStoresTheCommandError(t *testing.T) {
	var reason string
	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, id uuid.UUID) (*db.Deployment, error) { return &db.Deployment{ID: id}, nil },
		SetFailedFn: func(_ context.Context, _ uuid.UUID, msg string, _ *time.Time) error {
			reason = msg
			return nil
		},
	}

	m := makeStateMachine(store, nil, nil)
	cmd := &db.Command{ID: uuid.New(), DeploymentID: uuid.New(), Status: db.CommandStatusFailed, Error: "helm failed"}
	if err := m.advanceOnHelmResult(context.Background(), cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reason != "helm failed" {
		t.Errorf("expected error 'helm failed', got %q", reason)
	}
}

func TestAdvanceOnHelmResult_DeploymentNotFoundReturnsNil(t *testing.T) {
	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(context.Context, uuid.UUID) (*db.Deployment, error) { return nil, api.ErrNotFound },
	}
	m := makeStateMachine(store, nil, nil)
	if err := m.advanceOnHelmResult(context.Background(), &db.Command{ID: uuid.New(), DeploymentID: uuid.New()}); err != nil {
		t.Errorf("expected nil on not-found, got %v", err)
	}
}

// --- startHelm ---

func TestStartHelm_CreatesLinksThenPublishes(t *testing.T) {
	deployment := testDeployment()
	var calls []string
	var created *db.Command
	store := &testutil.MockDeploymentInfoStore{
		GetDepOutputsByDeploymentFn: func(context.Context, uuid.UUID) (map[string]json.RawMessage, error) { return nil, nil },
		CreateCommandFn: func(_ context.Context, cmd *db.Command) error {
			calls = append(calls, "create")
			created = cmd
			return nil
		},
		SetHelmCommandIDFn: func(_ context.Context, id uuid.UUID, cmdID uuid.UUID) error {
			calls = append(calls, "link")
			if id != deployment.ID || cmdID != created.ID {
				t.Errorf("expected %v linked to %v, got %v linked to %v", deployment.ID, created.ID, id, cmdID)
			}
			return nil
		},
	}
	topics := &testutil.MockTopics{}
	_, _ = topics.CommandQueued.For(testClusterID).Subscribe(context.Background(), func(context.Context, events.CommandQueued) {
		calls = append(calls, "publish")
	})

	m := makeStateMachine(store, nil, topics)
	if err := m.startHelm(context.Background(), deployment); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(calls, []string{"create", "link", "publish"}) {
		t.Errorf("expected create, link, publish; got %v", calls)
	}
	if created.Type != db.CommandTypeHelmUpgrade || created.ClusterID != testClusterID || created.Status != db.CommandStatusQueued {
		t.Errorf("unexpected command: %+v", created)
	}
}

func TestStartHelm_PublishErrorPropagates(t *testing.T) {
	publishErr := errors.New("broker down")
	store := &testutil.MockDeploymentInfoStore{}
	helmRecorder(store)
	topics := &testutil.MockTopics{}
	topics.CommandQueued.For(testClusterID).PublishErr = publishErr

	m := makeStateMachine(store, nil, topics)
	if err := m.startHelm(context.Background(), testDeployment()); !errors.Is(err, publishErr) {
		t.Fatalf("expected publish error, got %v", err)
	}
}

func TestRenderConfigs_SubstitutesDependencyOutputs(t *testing.T) {
	cfg := serviceconfig.ServiceConfig{
		Name: "svc",
		Configs: map[string]serviceconfig.ConfigFileSpec{
			"app": {Values: map[string]any{"db_host": "{{db.host}}"}},
		},
	}
	contexts := map[string]map[string]any{"db": {"host": "db.internal"}}

	rendered, err := renderConfigs(NewTemplateRenderer(), cfg, contexts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rendered.Configs["app"].Values["db_host"]; got != "db.internal" {
		t.Errorf("expected db.internal, got %v", got)
	}
	if cfg.Configs["app"].Values["db_host"] != "{{db.host}}" {
		t.Error("renderConfigs must not modify its input")
	}
}
