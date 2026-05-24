package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/server/internal/events"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/service"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/server/api"
)

type mockAgentConnectionService struct {
	newSessionFn func(clusterID uuid.UUID, log *slog.Logger) service.AgentSession
}

func (m *mockAgentConnectionService) NewSession(clusterID uuid.UUID, log *slog.Logger) service.AgentSession {
	if m.newSessionFn != nil {
		return m.newSessionFn(clusterID, log)
	}
	return &mockAgentSession{}
}

type mockAgentSession struct {
	startFn       func(ctx context.Context) (<-chan *domain.Command, <-chan struct{}, error)
	requestNextFn func(ctx context.Context) *domain.Command
	onAckFn       func(ctx context.Context, deploymentID uuid.UUID, commandID uuid.UUID)
	onResultFn    func(ctx context.Context, result events.CommandResult)
	onLogFn       func(ctx context.Context, commandID uuid.UUID, line string)
	closeFn       func(inFlightCommandID uuid.UUID)
}

func (m *mockAgentSession) Start(ctx context.Context) (<-chan *domain.Command, <-chan struct{}, error) {
	if m.startFn != nil {
		return m.startFn(ctx)
	}
	return make(chan *domain.Command), make(chan struct{}), nil
}

func (m *mockAgentSession) RequestNext(ctx context.Context) *domain.Command {
	if m.requestNextFn != nil {
		return m.requestNextFn(ctx)
	}
	return nil
}

func (m *mockAgentSession) OnAck(ctx context.Context, deploymentID uuid.UUID, commandID uuid.UUID) {
	if m.onAckFn != nil {
		m.onAckFn(ctx, deploymentID, commandID)
	}
}

func (m *mockAgentSession) OnResult(ctx context.Context, result events.CommandResult) {
	if m.onResultFn != nil {
		m.onResultFn(ctx, result)
	}
}

func (m *mockAgentSession) OnLog(ctx context.Context, commandID uuid.UUID, line string) {
	if m.onLogFn != nil {
		m.onLogFn(ctx, commandID, line)
	}
}

func (m *mockAgentSession) Close(inFlightCommandID uuid.UUID) {
	if m.closeFn != nil {
		m.closeFn(inFlightCommandID)
	}
}

func TestAgentHandler_ServeWS_Auth(t *testing.T) {
	clusterRepo := &testutil.MockClusterRepository{}
	handler := NewAgentHandler(clusterRepo, &mockAgentConnectionService{}, slog.Default())

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
				return nil, api.ErrNotFound
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
