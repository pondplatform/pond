package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/events"
	"github.com/pondplatform/pond/internal/server/service"
	"github.com/pondplatform/pond/internal/testutil"
)

type MockDeploymentService struct {
	SubmitFn      func(ctx context.Context, req service.SubmitRequest) (*domain.Deployment, error)
	GetStatusFn   func(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	ValidateFn    func(ctx context.Context, req service.SubmitRequest) (*service.ValidationResult, error)
	MarkRunningFn func(ctx context.Context, id uuid.UUID) error
	StartFn       func(ctx context.Context)
}

func (m *MockDeploymentService) Submit(ctx context.Context, req service.SubmitRequest) (*domain.Deployment, error) {
	if m.SubmitFn != nil {
		return m.SubmitFn(ctx, req)
	}
	return nil, nil
}
func (m *MockDeploymentService) GetStatus(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	if m.GetStatusFn != nil {
		return m.GetStatusFn(ctx, id)
	}
	return nil, nil
}
func (m *MockDeploymentService) Validate(ctx context.Context, req service.SubmitRequest) (*service.ValidationResult, error) {
	if m.ValidateFn != nil {
		return m.ValidateFn(ctx, req)
	}
	return nil, nil
}
func (m *MockDeploymentService) MarkRunning(ctx context.Context, id uuid.UUID) error {
	if m.MarkRunningFn != nil {
		return m.MarkRunningFn(ctx, id)
	}
	return nil
}
func (m *MockDeploymentService) Start(ctx context.Context) {
	if m.StartFn != nil {
		m.StartFn(ctx)
	}
}

func TestAgentHandler_ServeWS_Auth(t *testing.T) {
	clusterRepo := &testutil.MockClusterRepository{}
	handler := NewAgentHandler(clusterRepo, nil, nil, nil, nil)

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
		hash := sha256hex(token)
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

func TestAgentHandler_ServeWS_Result(t *testing.T) {
	clusterRepo := &testutil.MockClusterRepository{}
	cmdRepo := &testutil.MockCommandRepository{}
	bus := &testutil.MockBus{}
	deploySvc := &MockDeploymentService{}
	handler := NewAgentHandler(clusterRepo, cmdRepo, nil, deploySvc, bus)

	token := "my-token"
	hash := sha256hex(token)
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

	// Wait for agent_handler to be ready (it starts a read loop)
	time.Sleep(10 * time.Millisecond)

	cmdID := uuid.New()

	// 1. Agent sends "ready"
	// 2. Handler calls Dequeue. We return a command.
	// 3. Handler sets activeCommandID = cmdID and sends command to agent.

	cmdRepo.DequeueFn = func(ctx context.Context, id uuid.UUID) (*domain.Command, error) {
		return &domain.Command{ID: cmdID, Type: "test.cmd"}, nil
	}

	readyMsg := wsEnvelope{Type: "ready"}
	if err := ws.WriteJSON(readyMsg); err != nil {
		t.Fatalf("failed to write ready: %v", err)
	}

	// Read the command sent by the server
	var cmdEnv wsEnvelope
	if err := ws.ReadJSON(&cmdEnv); err != nil {
		t.Fatalf("failed to read command: %v", err)
	}

	// 4. Now send the "result" for that command
	resultMsg := wsEnvelope{
		Type: "result",
		Data: []byte(`{"command_id":"` + cmdID.String() + `","success":true,"output":"eyJwcm92aWRlciI6Im9rIn0="}`), // base64 for {"provider":"ok"}
	}

	var publishedResult events.CommandResult
	publishDone := make(chan struct{})
	bus.PublishFn = func(ctx context.Context, topic string, v any) {
		if topic == events.TopicCommandResults {
			publishedResult = v.(events.CommandResult)
			close(publishDone)
		}
	}

	// Also mock CommandRepository.MarkSucceeded which is called on "result"
	cmdRepo.MarkSucceededFn = func(ctx context.Context, id uuid.UUID, output json.RawMessage) error {
		return nil
	}
	// Subsequent dequeues return nil
	cmdRepo.DequeueFn = func(ctx context.Context, id uuid.UUID) (*domain.Command, error) {
		return nil, nil
	}

	if err := ws.WriteJSON(resultMsg); err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	select {
	case <-publishDone:
		if publishedResult.CommandID != cmdID {
			t.Errorf("expected commandID %v, got %v", cmdID, publishedResult.CommandID)
		}
		if !publishedResult.Success {
			t.Error("expected success to be true")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for publish")
	}
}
