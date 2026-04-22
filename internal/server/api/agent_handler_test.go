package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/auth"
	"github.com/pondplatform/pond/internal/server/events"
	"github.com/pondplatform/pond/internal/testutil"
)

func TestAgentHandler_ServeWS_Auth(t *testing.T) {
	clusterRepo := &testutil.MockClusterRepository{}
	handler := NewAgentHandler(clusterRepo, events.NewMemoryBus())

	t.Run("Unauthorized - no token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ws", nil)
		rr := httptest.NewRecorder()

		handler.ServeWS(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("Unauthorized - invalid token", func(t *testing.T) {
		token := "my-token"
		hash := auth.SHA256Hex(token)
		clusterRepo.GetByTokenHashFn = func(ctx context.Context, h string) (*domain.Cluster, error) {
			if h == hash {
				return nil, domain.ErrNotFound
			}
			return nil, nil
		}

		req := httptest.NewRequest("GET", "/ws", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()

		handler.ServeWS(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})
}

// TestAgentHandler_EventFlow verifies the full event-driven cycle:
//  1. Agent sends "ready" → handler publishes AgentReady → fake service
//     subscriber publishes CommandDispatch → handler writes a "command" frame.
//  2. Agent sends "ack" → handler publishes CommandStarted.
//  3. Agent sends "result" → handler publishes CommandResult and asks for
//     the next command (none queued → "idle" frame).
func TestAgentHandler_EventFlow(t *testing.T) {
	clusterRepo := &testutil.MockClusterRepository{}
	bus := events.NewMemoryBus()
	handler := NewAgentHandler(clusterRepo, bus)

	token := "my-token"
	hash := auth.SHA256Hex(token)
	clusterID := uuid.New()
	clusterRepo.GetByTokenHashFn = func(ctx context.Context, h string) (*domain.Cluster, error) {
		if h == hash {
			return &domain.Cluster{ID: clusterID}, nil
		}
		return nil, domain.ErrNotFound
	}
	clusterRepo.UpdateLastSeenFn = func(ctx context.Context, id uuid.UUID, lastSeen time.Time) error {
		return nil
	}

	// Fake deployment service: holds a queue of commands and drains one per
	// AgentReady, publishing CommandDispatch back onto the cluster topic.
	var (
		queueMu sync.Mutex
		queue   []*domain.Command
	)
	enqueue := func(cmd *domain.Command) {
		queueMu.Lock()
		queue = append(queue, cmd)
		queueMu.Unlock()
	}
	dequeue := func() *domain.Command {
		queueMu.Lock()
		defer queueMu.Unlock()
		if len(queue) == 0 {
			return nil
		}
		cmd := queue[0]
		queue = queue[1:]
		return cmd
	}

	bus.Subscribe(events.TopicAgentReady, func(v any) {
		ready, ok := v.(events.AgentReady)
		if !ok {
			return
		}
		cmd := dequeue()
		if cmd == nil {
			return
		}
		bus.Publish(context.Background(), events.ClusterCommandDispatchTopic(ready.ClusterID), events.CommandDispatch{Cmd: cmd})
	})

	var (
		startedMu    sync.Mutex
		commandStart []events.CommandStarted
	)
	bus.Subscribe(events.TopicCommandStarted, func(v any) {
		if e, ok := v.(events.CommandStarted); ok {
			startedMu.Lock()
			commandStart = append(commandStart, e)
			startedMu.Unlock()
		}
	})

	resultCh := make(chan events.CommandResult, 1)
	bus.Subscribe(events.TopicCommandResults, func(v any) {
		if e, ok := v.(events.CommandResult); ok {
			resultCh <- e
		}
	})

	cmdID := uuid.New()
	deploymentID := uuid.New()
	enqueue(&domain.Command{ID: cmdID, DeploymentID: deploymentID, Type: "test.cmd"})

	ts := httptest.NewServer(http.HandlerFunc(handler.ServeWS))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer ws.Close()

	// Agent → "ready"
	if err := ws.WriteJSON(wsEnvelope{Type: "ready"}); err != nil {
		t.Fatalf("write ready: %v", err)
	}

	// Server → "command"
	var cmdEnv wsEnvelope
	if err := ws.ReadJSON(&cmdEnv); err != nil {
		t.Fatalf("read command: %v", err)
	}
	if cmdEnv.Type != "command" {
		t.Fatalf("expected command, got %q", cmdEnv.Type)
	}

	// Agent → "ack"
	ackPayload := []byte(`{"command_id":"` + cmdID.String() + `","deployment_id":"` + deploymentID.String() + `"}`)
	if err := ws.WriteJSON(wsEnvelope{Type: "ack", Data: ackPayload}); err != nil {
		t.Fatalf("write ack: %v", err)
	}

	// Agent → "result"
	resultPayload := []byte(`{"success":true}`)
	if err := ws.WriteJSON(wsEnvelope{Type: "result", Data: resultPayload}); err != nil {
		t.Fatalf("write result: %v", err)
	}

	// Server → "idle" (queue is empty after the result)
	var idleEnv wsEnvelope
	if err := ws.ReadJSON(&idleEnv); err != nil {
		t.Fatalf("read idle: %v", err)
	}
	if idleEnv.Type != "idle" {
		t.Fatalf("expected idle, got %q", idleEnv.Type)
	}

	select {
	case got := <-resultCh:
		if got.CommandID != cmdID {
			t.Errorf("result commandID: want %v, got %v", cmdID, got.CommandID)
		}
		if !got.Success {
			t.Error("expected success")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for CommandResult")
	}

	startedMu.Lock()
	defer startedMu.Unlock()
	if len(commandStart) != 1 || commandStart[0].DeploymentID != deploymentID {
		t.Errorf("expected one CommandStarted for deployment %v, got %+v", deploymentID, commandStart)
	}
}
