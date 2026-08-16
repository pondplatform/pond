package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/auth"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

// --- test doubles ---

type mockAuthenticator struct {
	identity *domain.Identity
	err      error
}

func (m *mockAuthenticator) Authenticate(_ context.Context, _ *http.Request) (*domain.Identity, error) {
	return m.identity, m.err
}

type mockAuthorizer struct {
	err error
}

func (m *mockAuthorizer) Authorize(_ context.Context, _ *domain.Identity, _ auth.Action) error {
	return m.err
}

// okHandler is a simple handler that writes 200.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// --- requireAuth ---

func TestRequireAuth_NoToken_Returns401(t *testing.T) {
	mw := requireAuth(&mockAuthenticator{err: api.ErrUnauthorized}, slog.Default())
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuth_InvalidToken_Returns401(t *testing.T) {
	mw := requireAuth(&mockAuthenticator{err: api.ErrUnauthorized}, slog.Default())
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer bad-token")
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuth_ValidToken_InjectsIdentityAndCallsNext(t *testing.T) {
	expectedIdentity := &domain.Identity{Role: domain.RoleMember}
	mw := requireAuth(&mockAuthenticator{identity: expectedIdentity}, slog.Default())

	var capturedIdentity *domain.Identity
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedIdentity, _ = IdentityFromContext(r.Context())
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer valid")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if capturedIdentity == nil {
		t.Fatal("expected identity in context, got nil")
	}
	if capturedIdentity.Role != domain.RoleMember {
		t.Errorf("expected member role, got %s", capturedIdentity.Role)
	}
}

func TestRequireAuth_UnexpectedError_Returns500(t *testing.T) {
	mw := requireAuth(&mockAuthenticator{err: errors.New("db down")}, slog.Default())
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// --- requireResourceAccess ---

func requestWithIdentity(identity *domain.Identity) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	return r.WithContext(contextWithIdentity(r.Context(), identity))
}

func TestRequireResourceAccess_NoIdentityInContext_Returns500(t *testing.T) {
	mw := requireResourceAccess(
		&mockAuthorizer{},
		auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbRead},
		"",
		slog.Default(),
	)
	rec := httptest.NewRecorder()
	// Plain request — no identity injected
	mw(okHandler).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when identity missing, got %d", rec.Code)
	}
}

func TestRequireResourceAccess_Forbidden_Returns403(t *testing.T) {
	mw := requireResourceAccess(
		&mockAuthorizer{err: api.ErrForbidden},
		auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbWrite},
		"",
		slog.Default(),
	)
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, requestWithIdentity(&domain.Identity{Role: domain.RoleViewer}))
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestRequireResourceAccess_Allowed_CallsNext(t *testing.T) {
	mw := requireResourceAccess(
		&mockAuthorizer{err: nil},
		auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbRead},
		"",
		slog.Default(),
	)
	var nextCalled bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { nextCalled = true })

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, requestWithIdentity(&domain.Identity{Role: domain.RoleMember}))
	if !nextCalled {
		t.Error("expected next to be called when authorized")
	}
}

func TestRequireResourceAccess_InvalidResourceID_Returns400(t *testing.T) {
	mw := requireResourceAccess(
		&mockAuthorizer{},
		auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbRead},
		"deploymentId",
		slog.Default(),
	)
	req := httptest.NewRequest("GET", "/deployments/not-a-uuid", nil)
	req = req.WithContext(contextWithIdentity(req.Context(), &domain.Identity{Role: domain.RoleMember}))
	// PathValue requires the router to set the path param; simulate by using
	// a mux pattern match so PathValue works.
	mux := http.NewServeMux()
	mux.Handle("GET /deployments/{deploymentId}", mw(okHandler))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid UUID, got %d", rec.Code)
	}
}

func TestRequireResourceAccess_ValidResourceID_PopulatesAction(t *testing.T) {
	resourceID := uuid.New()
	var capturedAction auth.Action

	authorizer := &captureAuthorizer{}
	mw := requireResourceAccess(
		authorizer,
		auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbRead},
		"deploymentId",
		slog.Default(),
	)

	mux := http.NewServeMux()
	mux.Handle("GET /deployments/{deploymentId}", mw(okHandler))

	req := httptest.NewRequest("GET", "/deployments/"+resourceID.String(), nil)
	req = req.WithContext(contextWithIdentity(req.Context(), &domain.Identity{Role: domain.RoleMember}))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	capturedAction = authorizer.lastAction
	if capturedAction.ResourceID != resourceID {
		t.Errorf("expected resourceID %v, got %v", resourceID, capturedAction.ResourceID)
	}
}

// captureAuthorizer records the action it was called with.
type captureAuthorizer struct {
	lastAction auth.Action
}

func (c *captureAuthorizer) Authorize(_ context.Context, _ *domain.Identity, action auth.Action) error {
	c.lastAction = action
	return nil
}
