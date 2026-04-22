package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/auth"
	"github.com/pondplatform/pond/internal/testutil"
)

func TestAgentHandler_ServeWS_Auth(t *testing.T) {
	clusterRepo := &testutil.MockClusterRepository{}
	handler := NewAgentHandler(clusterRepo, &testutil.MockBus{})

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
