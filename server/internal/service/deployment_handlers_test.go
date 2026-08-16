package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/testutil"
)

// --- handleAgentReady ---

func TestHandleAgentReady_NoCommandsDoesNotPublish(t *testing.T) {
	store := &testutil.MockDeploymentInfoStore{
		ListQueuedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*domain.Command, error) {
			return nil, nil
		},
	}
	var published bool
	bus := &testutil.MockBus{PublishFn: func(_ context.Context, _ string, _ any) { published = true }}
	svc := makeAdvanceSvc(store, nil, nil, nil, bus)
	svc.handleAgentReady(context.Background(), events.AgentReady{ClusterID: uuid.New()})
	if published {
		t.Error("expected no publish when there are no queued commands")
	}
}

func TestHandleAgentReady_DispatchesFirstQueuedCommand(t *testing.T) {
	clusterID := uuid.New()
	cmdID := uuid.New()
	cmd := &domain.Command{ID: cmdID, ClusterID: clusterID, Status: domain.CommandStatusQueued}

	var updatedStatus domain.CommandStatus
	store := &testutil.MockDeploymentInfoStore{
		ListQueuedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*domain.Command, error) {
			return []*domain.Command{cmd}, nil
		},
		UpdateCommandFn: func(_ context.Context, c *domain.Command) error {
			updatedStatus = c.Status
			return nil
		},
	}

	var publishedTopic string
	var publishedEvent any
	bus := &testutil.MockBus{
		PublishFn: func(_ context.Context, topic string, v any) {
			publishedTopic = topic
			publishedEvent = v
		},
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, bus)
	svc.handleAgentReady(context.Background(), events.AgentReady{ClusterID: clusterID})

	if updatedStatus != domain.CommandStatusDispatched {
		t.Errorf("expected command transitioned to dispatched, got %s", updatedStatus)
	}
	expectedTopic := events.ClusterCommandDispatchTopic(clusterID)
	if publishedTopic != expectedTopic {
		t.Errorf("expected topic %s, got %s", expectedTopic, publishedTopic)
	}
	dispatch, ok := publishedEvent.(events.CommandDispatch)
	if !ok {
		t.Fatalf("expected CommandDispatch event, got %T", publishedEvent)
	}
	if dispatch.Cmd.ID != cmdID {
		t.Errorf("expected cmdID %v in dispatch, got %v", cmdID, dispatch.Cmd.ID)
	}
}

func TestHandleAgentReady_DispatchesOnlyFirstCommand(t *testing.T) {
	clusterID := uuid.New()
	cmds := []*domain.Command{
		{ID: uuid.New(), ClusterID: clusterID, Status: domain.CommandStatusQueued},
		{ID: uuid.New(), ClusterID: clusterID, Status: domain.CommandStatusQueued},
	}

	var updateCount int
	store := &testutil.MockDeploymentInfoStore{
		ListQueuedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*domain.Command, error) {
			return cmds, nil
		},
		UpdateCommandFn: func(_ context.Context, _ *domain.Command) error {
			updateCount++
			return nil
		},
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	svc.handleAgentReady(context.Background(), events.AgentReady{ClusterID: clusterID})
	if updateCount != 1 {
		t.Errorf("expected exactly 1 command updated, got %d", updateCount)
	}
}

// --- handleAgentDisconnected ---

func TestHandleAgentDisconnected_RequeuesDispatchedCommands(t *testing.T) {
	clusterID := uuid.New()
	cmds := []*domain.Command{
		{ID: uuid.New(), Status: domain.CommandStatusDispatched},
		{ID: uuid.New(), Status: domain.CommandStatusDispatched},
	}

	var requeued []uuid.UUID
	store := &testutil.MockDeploymentInfoStore{
		ListDispatchedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*domain.Command, error) {
			return cmds, nil
		},
		UpdateCommandFn: func(_ context.Context, c *domain.Command) error {
			if c.Status != domain.CommandStatusQueued {
				t.Errorf("expected queued, got %s", c.Status)
			}
			requeued = append(requeued, c.ID)
			return nil
		},
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	svc.handleAgentDisconnected(context.Background(), events.AgentDisconnected{ClusterID: clusterID})

	if len(requeued) != 2 {
		t.Errorf("expected 2 commands requeued, got %d", len(requeued))
	}
}

func TestHandleAgentDisconnected_NoCommandsIsNoop(t *testing.T) {
	store := &testutil.MockDeploymentInfoStore{
		ListDispatchedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*domain.Command, error) {
			return nil, nil
		},
	}
	var updateCalled bool
	store.UpdateCommandFn = func(_ context.Context, _ *domain.Command) error {
		updateCalled = true
		return nil
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	svc.handleAgentDisconnected(context.Background(), events.AgentDisconnected{ClusterID: uuid.New()})
	if updateCalled {
		t.Error("expected no update when no dispatched commands")
	}
}

func TestHandleAgentDisconnected_UpdatesTimestamp(t *testing.T) {
	clusterID := uuid.New()
	before := time.Now()

	cmd := &domain.Command{ID: uuid.New(), Status: domain.CommandStatusDispatched, UpdatedAt: before.Add(-time.Hour)}
	store := &testutil.MockDeploymentInfoStore{
		ListDispatchedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*domain.Command, error) {
			return []*domain.Command{cmd}, nil
		},
		UpdateCommandFn: func(_ context.Context, c *domain.Command) error {
			if !c.UpdatedAt.After(before.Add(-time.Minute)) {
				t.Errorf("expected UpdatedAt to be refreshed, got %v", c.UpdatedAt)
			}
			return nil
		},
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	svc.handleAgentDisconnected(context.Background(), events.AgentDisconnected{ClusterID: clusterID})
}

// --- handleCommandLog ---

func TestHandleCommandLog_AppendsLineToStore(t *testing.T) {
	cmdID := uuid.New()
	var appendedCommandID uuid.UUID
	var appendedLine string

	store := &testutil.MockDeploymentInfoStore{
		AppendLogFn: func(_ context.Context, commandID uuid.UUID, line string) error {
			appendedCommandID = commandID
			appendedLine = line
			return nil
		},
	}

	svc := makeAdvanceSvc(store, nil, nil, nil, nil)
	svc.handleCommandLog(context.Background(), events.CommandLog{CommandID: cmdID, Line: "hello world"})

	if appendedCommandID != cmdID {
		t.Errorf("expected commandID %v, got %v", cmdID, appendedCommandID)
	}
	if appendedLine != "hello world" {
		t.Errorf("expected 'hello world', got %q", appendedLine)
	}
}

// --- handleUserInputProvided ---

func TestHandleUserInputProvided_AdvancesDependencyStatus(t *testing.T) {
	ctx := context.Background()
	depID := uuid.New()
	envID := uuid.New()

	var scheduleCommandsCalled bool
	depSvc := &mockDependencyService{
		dependencyDeploymentStatusFn: func(_ context.Context, _ uuid.UUID) (domain.DependencyDeploymentStatus, error) {
			// Return pending so ScheduleCommands is called
			return domain.DependencyDeploymentStatusPending, nil
		},
		scheduleCommandsFn: func(_ context.Context, _ uuid.UUID) error {
			scheduleCommandsCalled = true
			return nil
		},
	}

	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{ID: depID, EnvironmentID: envID}, nil
		},
		UpdateStatusFn: func(_ context.Context, _ uuid.UUID, _ domain.DeploymentStatus, _ *time.Time) error { return nil },
	}

	envs := &testutil.MockEnvironmentRepository{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Environment, error) {
			return &domain.Environment{ID: envID, ClusterID: uuid.New()}, nil
		},
	}

	svc := makeAdvanceSvc(store, envs, depSvc, nil, nil)
	svc.handleUserInputProvided(ctx, events.UserInputProvided{DeploymentID: depID})

	if !scheduleCommandsCalled {
		t.Error("expected ScheduleCommands to be called via advanceDependencyStatus")
	}
}
