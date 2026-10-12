package service

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/testutil"
)

// --- handleAgentReady ---

func TestHandleAgentReady_NoCommandsDoesNotPublish(t *testing.T) {
	store := &testutil.MockDeploymentInfoStore{
		ListQueuedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*db.Command, error) {
			return nil, nil
		},
	}
	topics := &testutil.MockTopics{}
	router := makeEventRouter(store, nil, topics)
	router.handleAgentReady(context.Background(), events.AgentReady{ClusterID: uuid.New()})
	if n := topics.CommandDispatch.PublishedCount(); n != 0 {
		t.Errorf("expected no publish when there are no queued commands, got %d", n)
	}
}

func TestHandleAgentReady_DispatchesFirstQueuedCommand(t *testing.T) {
	clusterID := uuid.New()
	cmdID := uuid.New()
	cmd := &db.Command{ID: cmdID, ClusterID: clusterID, Status: db.CommandStatusQueued}

	var updatedStatus db.CommandStatus
	store := &testutil.MockDeploymentInfoStore{
		ListQueuedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*db.Command, error) {
			return []*db.Command{cmd}, nil
		},
		UpdateCommandFn: func(_ context.Context, c *db.Command) error {
			updatedStatus = c.Status
			return nil
		},
	}

	topics := &testutil.MockTopics{}
	router := makeEventRouter(store, nil, topics)
	router.handleAgentReady(context.Background(), events.AgentReady{ClusterID: clusterID})

	if updatedStatus != db.CommandStatusDispatched {
		t.Errorf("expected command transitioned to dispatched, got %s", updatedStatus)
	}
	published := topics.CommandDispatch.For(clusterID).Published()
	if len(published) != 1 {
		t.Fatalf("expected 1 CommandDispatch for cluster, got %d", len(published))
	}
	if published[0].Cmd.ID != cmdID {
		t.Errorf("expected cmdID %v in dispatch, got %v", cmdID, published[0].Cmd.ID)
	}
}

func TestHandleAgentReady_PublishErrorRevertsToQueued(t *testing.T) {
	clusterID := uuid.New()
	cmd := &db.Command{ID: uuid.New(), ClusterID: clusterID, Status: db.CommandStatusQueued}

	var statuses []db.CommandStatus
	store := &testutil.MockDeploymentInfoStore{
		ListQueuedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*db.Command, error) {
			return []*db.Command{cmd}, nil
		},
		UpdateCommandFn: func(_ context.Context, c *db.Command) error {
			statuses = append(statuses, c.Status)
			return nil
		},
	}
	topics := &testutil.MockTopics{}
	topics.CommandDispatch.For(clusterID).PublishErr = errors.New("broker down")

	router := makeEventRouter(store, nil, topics)
	router.handleAgentReady(context.Background(), events.AgentReady{ClusterID: clusterID})

	want := []db.CommandStatus{db.CommandStatusDispatched, db.CommandStatusQueued}
	if !slices.Equal(statuses, want) {
		t.Errorf("expected status updates %v, got %v", want, statuses)
	}
}

func TestHandleAgentReady_DispatchesOnlyFirstCommand(t *testing.T) {
	clusterID := uuid.New()
	cmds := []*db.Command{
		{ID: uuid.New(), ClusterID: clusterID, Status: db.CommandStatusQueued},
		{ID: uuid.New(), ClusterID: clusterID, Status: db.CommandStatusQueued},
	}

	var updateCount int
	store := &testutil.MockDeploymentInfoStore{
		ListQueuedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*db.Command, error) {
			return cmds, nil
		},
		UpdateCommandFn: func(_ context.Context, _ *db.Command) error {
			updateCount++
			return nil
		},
	}

	router := makeEventRouter(store, nil, nil)
	router.handleAgentReady(context.Background(), events.AgentReady{ClusterID: clusterID})
	if updateCount != 1 {
		t.Errorf("expected exactly 1 command updated, got %d", updateCount)
	}
}

// --- handleAgentDisconnected ---

func TestHandleAgentDisconnected_RequeuesDispatchedCommands(t *testing.T) {
	clusterID := uuid.New()
	cmds := []*db.Command{
		{ID: uuid.New(), Status: db.CommandStatusDispatched},
		{ID: uuid.New(), Status: db.CommandStatusDispatched},
	}

	var requeued []uuid.UUID
	store := &testutil.MockDeploymentInfoStore{
		ListDispatchedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*db.Command, error) {
			return cmds, nil
		},
		UpdateCommandFn: func(_ context.Context, c *db.Command) error {
			if c.Status != db.CommandStatusQueued {
				t.Errorf("expected queued, got %s", c.Status)
			}
			requeued = append(requeued, c.ID)
			return nil
		},
	}

	router := makeEventRouter(store, nil, nil)
	router.handleAgentDisconnected(context.Background(), events.AgentDisconnected{ClusterID: clusterID})

	if len(requeued) != 2 {
		t.Errorf("expected 2 commands requeued, got %d", len(requeued))
	}
}

func TestHandleAgentDisconnected_NoCommandsIsNoop(t *testing.T) {
	store := &testutil.MockDeploymentInfoStore{
		ListDispatchedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*db.Command, error) {
			return nil, nil
		},
	}
	var updateCalled bool
	store.UpdateCommandFn = func(_ context.Context, _ *db.Command) error {
		updateCalled = true
		return nil
	}

	router := makeEventRouter(store, nil, nil)
	router.handleAgentDisconnected(context.Background(), events.AgentDisconnected{ClusterID: uuid.New()})
	if updateCalled {
		t.Error("expected no update when no dispatched commands")
	}
}

func TestHandleAgentDisconnected_UpdatesTimestamp(t *testing.T) {
	clusterID := uuid.New()
	before := time.Now()

	cmd := &db.Command{ID: uuid.New(), Status: db.CommandStatusDispatched, UpdatedAt: before.Add(-time.Hour)}
	store := &testutil.MockDeploymentInfoStore{
		ListDispatchedCommandsByClusterFn: func(_ context.Context, _ uuid.UUID) ([]*db.Command, error) {
			return []*db.Command{cmd}, nil
		},
		UpdateCommandFn: func(_ context.Context, c *db.Command) error {
			if !c.UpdatedAt.After(before.Add(-time.Minute)) {
				t.Errorf("expected UpdatedAt to be refreshed, got %v", c.UpdatedAt)
			}
			return nil
		},
	}

	router := makeEventRouter(store, nil, nil)
	router.handleAgentDisconnected(context.Background(), events.AgentDisconnected{ClusterID: clusterID})
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

	router := makeEventRouter(store, nil, nil)
	router.handleCommandLog(context.Background(), events.CommandLog{CommandID: cmdID, Line: "hello world"})

	if appendedCommandID != cmdID {
		t.Errorf("expected commandID %v, got %v", cmdID, appendedCommandID)
	}
	if appendedLine != "hello world" {
		t.Errorf("expected 'hello world', got %q", appendedLine)
	}
}

// --- handleCommandResult ---

type resultRecorderFunc func(ctx context.Context, result events.CommandResult) error

func (f resultRecorderFunc) RecordResult(ctx context.Context, result events.CommandResult) error {
	return f(ctx, result)
}

func TestHandleCommandResult_RecordsTheResult(t *testing.T) {
	result := events.CommandResult{CommandID: uuid.New(), Success: true}
	var got *events.CommandResult
	recorder := resultRecorderFunc(func(_ context.Context, r events.CommandResult) error {
		got = &r
		return errors.New("logged, not returned")
	})

	router := makeEventRouter(&testutil.MockDeploymentInfoStore{}, recorder, nil)
	router.handleCommandResult(context.Background(), result)

	if got == nil || got.CommandID != result.CommandID {
		t.Errorf("expected result %v forwarded, got %+v", result.CommandID, got)
	}
}

func makeEventRouter(store *testutil.MockDeploymentInfoStore, recorder commandResultRecorder, topics *testutil.MockTopics) *EventRouter {
	if recorder == nil {
		recorder = resultRecorderFunc(func(context.Context, events.CommandResult) error { return nil })
	}
	if topics == nil {
		topics = &testutil.MockTopics{}
	}
	return &EventRouter{deploymentInfo: store, results: recorder, topics: topics, log: slog.Default()}
}
