package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// makeAdvanceSvc builds a deploymentService wired for state-machine tests.
func makeAdvanceSvc(
	store *testutil.MockDeploymentInfoStore,
	envs *testutil.MockEnvironmentRepository,
	depSvc DependencyService,
	helmGen *testutil.MockHelmValuesGenerator,
	bus *testutil.MockBus,
) *deploymentService {
	if envs == nil {
		envs = &testutil.MockEnvironmentRepository{}
	}
	if depSvc == nil {
		depSvc = &mockDependencyService{}
	}
	if helmGen == nil {
		helmGen = &testutil.MockHelmValuesGenerator{}
	}
	if bus == nil {
		bus = &testutil.MockBus{}
	}
	return &deploymentService{
		deploymentInfo: store,
		envs:           envs,
		depSvc:         depSvc,
		helmGen:        helmGen,
		tmplRenderer:   NewTemplateRenderer(),
		bus:            bus,
		log:            slog.Default(),
	}
}

// --- processResult ---

func TestProcessResult_UpdatesCommandStatusOnSuccess(t *testing.T) {
	ctx := context.Background()
	cmdID := uuid.New()
	depID := uuid.New()

	cmd := &domain.Command{
		ID:           cmdID,
		DeploymentID: depID,
		Type:         domain.CommandTypeHelmUpgrade,
	}

	var updatedCmd *domain.Command
	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn: func(_ context.Context, _ uuid.UUID) (*domain.Command, error) {
			return cmd, nil
		},
		UpdateCommandFn: func(_ context.Context, c *domain.Command) error {
			updatedCmd = c
			return nil
		},
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{ID: depID}, nil
		},
		UpdateStatusFn: func(_ context.Context, _ uuid.UUID, _ domain.DeploymentStatus, _ *time.Time) error {
			return nil
		},
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	if err := svc.processResult(ctx, events.CommandResult{CommandID: cmdID, Success: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updatedCmd == nil {
		t.Fatal("UpdateCommand was not called")
	}
	if updatedCmd.Status != domain.CommandStatusSucceeded {
		t.Errorf("expected succeeded, got %s", updatedCmd.Status)
	}
}

func TestProcessResult_UpdatesCommandStatusOnFailure(t *testing.T) {
	ctx := context.Background()
	cmdID := uuid.New()
	depID := uuid.New()

	cmd := &domain.Command{
		ID:           cmdID,
		DeploymentID: depID,
		Type:         domain.CommandTypeHelmUpgrade,
	}

	var updatedCmd *domain.Command
	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn: func(_ context.Context, _ uuid.UUID) (*domain.Command, error) {
			return cmd, nil
		},
		UpdateCommandFn: func(_ context.Context, c *domain.Command) error {
			updatedCmd = c
			return nil
		},
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{ID: depID}, nil
		},
		SetFailedFn: func(_ context.Context, _ uuid.UUID, _ string, _ *time.Time) error {
			return nil
		},
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	if err := svc.processResult(ctx, events.CommandResult{CommandID: cmdID, Success: false, Error: "boom"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updatedCmd == nil {
		t.Fatal("UpdateCommand was not called")
	}
	if updatedCmd.Status != domain.CommandStatusFailed {
		t.Errorf("expected failed, got %s", updatedCmd.Status)
	}
	if updatedCmd.Error != "boom" {
		t.Errorf("expected error 'boom', got %q", updatedCmd.Error)
	}
}

func TestProcessResult_GetCommandErrorPropagates(t *testing.T) {
	ctx := context.Background()
	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn: func(_ context.Context, _ uuid.UUID) (*domain.Command, error) {
			return nil, errors.New("db error")
		},
	}
	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	if err := svc.processResult(ctx, events.CommandResult{CommandID: uuid.New()}); err == nil {
		t.Fatal("expected error when GetCommand fails")
	}
}

func TestProcessResult_TofuApplyRoutesToHandleCommandResult(t *testing.T) {
	ctx := context.Background()
	cmdID := uuid.New()
	depID := uuid.New()

	var handleResultCalled bool
	depSvc := &mockDependencyService{
		handleCommandResultFn: func(_ context.Context, _ uuid.UUID) error {
			handleResultCalled = true
			return nil
		},
		dependencyDeploymentStatusFn: func(_ context.Context, _ uuid.UUID) (domain.DependencyDeploymentStatus, error) {
			return domain.DependencyDeploymentStatusSucceeded, nil
		},
	}

	store := &testutil.MockDeploymentInfoStore{
		GetCommandFn: func(_ context.Context, _ uuid.UUID) (*domain.Command, error) {
			return &domain.Command{ID: cmdID, DeploymentID: depID, Type: domain.CommandTypeTofuApply}, nil
		},
		UpdateCommandFn: func(_ context.Context, _ *domain.Command) error { return nil },
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{ID: depID, ServiceConfigSnapshot: serviceconfig.ServiceConfig{Name: "svc"}}, nil
		},
		UpdateStatusFn:              func(_ context.Context, _ uuid.UUID, _ domain.DeploymentStatus, _ *time.Time) error { return nil },
		GetDepOutputsByDeploymentFn: func(_ context.Context, _ uuid.UUID) (map[string]json.RawMessage, error) { return nil, nil },
		SetHelmCommandIDFn:          func(_ context.Context, _ uuid.UUID, _ uuid.UUID) error { return nil },
		CreateCommandFn:             func(_ context.Context, _ *domain.Command) error { return nil },
	}

	envs := &testutil.MockEnvironmentRepository{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Environment, error) {
			return &domain.Environment{ID: uuid.New(), ClusterID: uuid.New()}, nil
		},
	}

	svc := makeAdvanceSvc(store, envs, depSvc, nil, nil)
	if err := svc.processResult(ctx, events.CommandResult{CommandID: cmdID, Success: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handleResultCalled {
		t.Error("expected HandleCommandResult to be called for tofu.apply command")
	}
}

// --- advanceHelmUpgrade ---

func TestAdvanceHelmUpgrade_SuccessTransitionsToSucceeded(t *testing.T) {
	ctx := context.Background()
	depID := uuid.New()

	var capturedStatus domain.DeploymentStatus
	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{ID: depID}, nil
		},
		UpdateStatusFn: func(_ context.Context, _ uuid.UUID, status domain.DeploymentStatus, _ *time.Time) error {
			capturedStatus = status
			return nil
		},
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	err := svc.advanceHelmUpgrade(ctx, &domain.Command{ID: uuid.New(), DeploymentID: depID}, events.CommandResult{Success: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedStatus != api.DeploymentStatusSucceeded {
		t.Errorf("expected succeeded, got %s", capturedStatus)
	}
}

func TestAdvanceHelmUpgrade_FailureCallsSetFailed(t *testing.T) {
	ctx := context.Background()
	depID := uuid.New()

	var capturedError string
	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{ID: depID}, nil
		},
		SetFailedFn: func(_ context.Context, _ uuid.UUID, errMsg string, _ *time.Time) error {
			capturedError = errMsg
			return nil
		},
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	err := svc.advanceHelmUpgrade(ctx, &domain.Command{ID: uuid.New(), DeploymentID: depID}, events.CommandResult{Success: false, Error: "helm failed"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedError != "helm failed" {
		t.Errorf("expected error 'helm failed', got %q", capturedError)
	}
}

func TestAdvanceHelmUpgrade_DeploymentNotFoundReturnsNil(t *testing.T) {
	ctx := context.Background()
	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return nil, api.ErrNotFound
		},
	}
	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	err := svc.advanceHelmUpgrade(ctx, &domain.Command{ID: uuid.New(), DeploymentID: uuid.New()}, events.CommandResult{Success: true})
	if err != nil {
		t.Errorf("expected nil on not-found, got %v", err)
	}
}

// --- advanceOnDependencyStatus ---

func TestAdvanceOnDependencyStatus_AwaitingInputSetsStatus(t *testing.T) {
	ctx := context.Background()

	var capturedStatus domain.DeploymentStatus
	store := &testutil.MockDeploymentInfoStore{
		UpdateStatusFn: func(_ context.Context, _ uuid.UUID, status domain.DeploymentStatus, _ *time.Time) error {
			capturedStatus = status
			return nil
		},
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	err := svc.advanceOnDependencyStatus(ctx, &domain.Deployment{ID: uuid.New()}, &domain.Environment{ID: uuid.New()}, domain.DependencyDeploymentStatusAwaitingInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedStatus != api.DeploymentStatusAwaitingInput {
		t.Errorf("expected awaiting_input, got %s", capturedStatus)
	}
}

func TestAdvanceOnDependencyStatus_FailedCallsSetFailed(t *testing.T) {
	ctx := context.Background()

	var failedCalled bool
	store := &testutil.MockDeploymentInfoStore{
		SetFailedFn: func(_ context.Context, _ uuid.UUID, _ string, _ *time.Time) error {
			failedCalled = true
			return nil
		},
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	err := svc.advanceOnDependencyStatus(ctx, &domain.Deployment{ID: uuid.New()}, &domain.Environment{}, domain.DependencyDeploymentStatusFailed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !failedCalled {
		t.Error("expected SetFailed to be called")
	}
}

func TestAdvanceOnDependencyStatus_PendingSchedulesCommands(t *testing.T) {
	ctx := context.Background()
	clusterID := uuid.New()

	var scheduleCalled bool
	depSvc := &mockDependencyService{
		scheduleCommandsFn: func(_ context.Context, _ uuid.UUID) error {
			scheduleCalled = true
			return nil
		},
	}

	var publishedTopic string
	bus := &testutil.MockBus{
		PublishFn: func(_ context.Context, topic string, _ any) { publishedTopic = topic },
	}

	store := &testutil.MockDeploymentInfoStore{
		UpdateStatusFn: func(_ context.Context, _ uuid.UUID, _ domain.DeploymentStatus, _ *time.Time) error { return nil },
	}

	svc := makeAdvanceSvc(store, nil, depSvc, nil, bus)
	env := &domain.Environment{ID: uuid.New(), ClusterID: clusterID}
	err := svc.advanceOnDependencyStatus(ctx, &domain.Deployment{ID: uuid.New()}, env, domain.DependencyDeploymentStatusPending)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !scheduleCalled {
		t.Error("expected ScheduleCommands to be called")
	}
	if publishedTopic != events.ClusterCommandQueuedTopic(clusterID) {
		t.Errorf("expected queued topic, got %s", publishedTopic)
	}
}

func TestAdvanceOnDependencyStatus_SucceededEnqueuesHelmCommand(t *testing.T) {
	ctx := context.Background()
	clusterID := uuid.New()

	var helmCreated bool
	var publishedTopic string

	store := &testutil.MockDeploymentInfoStore{
		UpdateStatusFn:              func(_ context.Context, _ uuid.UUID, _ domain.DeploymentStatus, _ *time.Time) error { return nil },
		GetDepOutputsByDeploymentFn: func(_ context.Context, _ uuid.UUID) (map[string]json.RawMessage, error) { return nil, nil },
		SetHelmCommandIDFn:          func(_ context.Context, _ uuid.UUID, _ uuid.UUID) error { return nil },
		CreateCommandFn: func(_ context.Context, _ *domain.Command) error {
			helmCreated = true
			return nil
		},
	}

	bus := &testutil.MockBus{
		PublishFn: func(_ context.Context, topic string, _ any) { publishedTopic = topic },
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, bus)
	dep := &domain.Deployment{
		ID:                    uuid.New(),
		ServiceConfigSnapshot: serviceconfig.ServiceConfig{Name: "my-svc"},
	}
	env := &domain.Environment{ID: uuid.New(), ClusterID: clusterID, Namespace: "default"}
	err := svc.advanceOnDependencyStatus(ctx, dep, env, domain.DependencyDeploymentStatusSucceeded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !helmCreated {
		t.Error("expected helm command to be created")
	}
	if publishedTopic != events.ClusterCommandQueuedTopic(clusterID) {
		t.Errorf("expected queued topic, got %s", publishedTopic)
	}
}
