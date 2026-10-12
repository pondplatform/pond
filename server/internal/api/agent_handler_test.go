package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pondplatform/pond/server/internal/auth"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/server/api"
)

func serveWS(handler *AgentHandler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := gin.New()
	r.GET("/ws", handler.ServeWS)
	r.ServeHTTP(rec, req)
	return rec
}

func TestAgentHandler_ServeWS_Auth(t *testing.T) {
	clusterRepo := &testutil.MockClusterRepository{}
	handler := NewAgentHandler(clusterRepo, nil, time.Second, slog.Default())

	t.Run("Unauthorized - no token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ws", nil)
		rr := serveWS(handler, req)
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
		rr := serveWS(handler, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})
}
