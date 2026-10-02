//go:build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/server/internal/events"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/service"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startRabbitMQ(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "rabbitmq:4-alpine",
		ExposedPorts: []string{"5672/tcp"},
		WaitingFor:   wait.ForLog("Server startup complete").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start rabbitmq container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("rabbitmq host: %v", err)
	}
	port, err := c.MappedPort(ctx, "5672")
	if err != nil {
		t.Fatalf("rabbitmq port: %v", err)
	}
	return fmt.Sprintf("amqp://guest:guest@%s:%s/", host, port.Port())
}

// TestAgentHandler_EventFlow verifies the full event-driven cycle with a real
// RabbitMQ broker:
//  1. Agent sends "ready" → handler publishes AgentReady → fake service
//     subscriber publishes CommandDispatch → handler writes a "command" frame.
//  2. Agent sends "ack" → handler publishes CommandStarted.
//  3. Agent sends "result" → handler publishes CommandResult and asks for
//     the next command (none queued → "idle" frame).
func TestAgentHandler_EventFlow(t *testing.T) {
	amqpURL := startRabbitMQ(t)
	topics, rmq, err := events.NewRabbitMQTopics(amqpURL, slog.Default())
	if err != nil {
		t.Fatalf("new rabbitmq topics: %v", err)
	}
	t.Cleanup(rmq.Close)
	ctx := context.Background()

	clusterRepo := &testutil.MockClusterRepository{}
	handler := NewAgentHandler(clusterRepo, service.NewAgentConnectionService(topics), slog.Default())

	token := "my-token"
	hash := auth.SHA256Hex(token)
	clusterID := uuid.New()
	clusterRepo.GetByTokenHashFn = func(ctx context.Context, h string) (*domain.Cluster, error) {
		if h == hash {
			return &domain.Cluster{ID: clusterID}, nil
		}
		return nil, api.ErrNotFound
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

	if _, err := topics.AgentReadySubscriber().Subscribe(ctx, func(ctx context.Context, ready events.AgentReady) {
		cmd := dequeue()
		if cmd == nil {
			return
		}
		if err := topics.CommandDispatchPublisher(ready.ClusterID).Publish(ctx, events.CommandDispatch{Cmd: cmd}); err != nil {
			t.Errorf("publish dispatch: %v", err)
		}
	}); err != nil {
		t.Fatalf("subscribe agent_ready: %v", err)
	}

	var (
		startedMu    sync.Mutex
		commandStart []events.CommandStarted
	)
	if _, err := topics.CommandStartedSubscriber().Subscribe(ctx, func(_ context.Context, e events.CommandStarted) {
		startedMu.Lock()
		commandStart = append(commandStart, e)
		startedMu.Unlock()
	}); err != nil {
		t.Fatalf("subscribe command_started: %v", err)
	}

	resultCh := make(chan events.CommandResult, 1)
	if _, err := topics.CommandResultsSubscriber().Subscribe(ctx, func(_ context.Context, e events.CommandResult) {
		resultCh <- e
	}); err != nil {
		t.Fatalf("subscribe command_results: %v", err)
	}

	cmdID := uuid.New()
	enqueue(&domain.Command{ID: cmdID, Type: "test.cmd"})

	router := gin.New()
	router.GET("/ws", handler.ServeWS)
	ts := httptest.NewServer(router)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer ws.Close()

	// Agent → "ready"
	if err := ws.WriteJSON(wire.Envelope{Type: "ready"}); err != nil {
		t.Fatalf("write ready: %v", err)
	}

	// Server → "command" (allow extra time for async dispatch)
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var cmdEnv wire.Envelope
	if err := ws.ReadJSON(&cmdEnv); err != nil {
		t.Fatalf("read command: %v", err)
	}
	if cmdEnv.Type != "command" {
		t.Fatalf("expected command, got %q", cmdEnv.Type)
	}

	// Agent → "ack"
	ackPayload, _ := json.Marshal(wire.AckPayload{CommandID: cmdID})
	if err := ws.WriteJSON(wire.Envelope{Type: "ack", Data: ackPayload}); err != nil {
		t.Fatalf("write ack: %v", err)
	}

	// Agent → "result"
	resultPayload, _ := json.Marshal(wire.ResultPayload{CommandID: cmdID, Success: true})
	if err := ws.WriteJSON(wire.Envelope{Type: "result", Data: resultPayload}); err != nil {
		t.Fatalf("write result: %v", err)
	}

	// Server → "idle" (queue is empty after the result)
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var idleEnv wire.Envelope
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
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for CommandResult")
	}

	// Give CommandStarted event time to arrive (async)
	deadline := time.Now().Add(5 * time.Second)
	for {
		startedMu.Lock()
		n := len(commandStart)
		startedMu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for CommandStarted")
		}
		time.Sleep(50 * time.Millisecond)
	}

	startedMu.Lock()
	defer startedMu.Unlock()
	if len(commandStart) != 1 || commandStart[0].CommandID != cmdID {
		t.Errorf("expected one CommandStarted for command %v, got %+v", cmdID, commandStart)
	}
}
