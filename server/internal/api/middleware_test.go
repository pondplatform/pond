package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/pondplatform/pond/server/internal/auth"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

func init() {
	gin.SetMode(gin.TestMode)
}

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

// ginContext creates a gin test context with the given request.
func ginContext(req *http.Request) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	return c, rec
}

// runMiddleware runs a single gin.HandlerFunc against a request, calling Next automatically.
func runMiddleware(mw gin.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, router := gin.CreateTestContext(rec)
	c.Request = req
	router.GET("/test", mw, func(c *gin.Context) { c.Status(http.StatusOK) })
	router.ServeHTTP(rec, req)
	return rec
}

// --- GinRequireAuth ---

func TestGinRequireAuth_NoToken_Returns401(t *testing.T) {
	rec := runMiddleware(GinRequireAuth(&mockAuthenticator{err: api.ErrUnauthorized}, nil), httptest.NewRequest("GET", "/test", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestGinRequireAuth_InvalidToken_Returns401(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer bad-token")
	rec := runMiddleware(GinRequireAuth(&mockAuthenticator{err: api.ErrUnauthorized}, nil), req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestGinRequireAuth_ValidToken_InjectsIdentityAndCallsNext(t *testing.T) {
	expectedIdentity := &domain.Identity{Role: domain.RoleMember}

	rec := httptest.NewRecorder()
	router := gin.New()
	var capturedIdentity *domain.Identity
	router.GET("/test",
		GinRequireAuth(&mockAuthenticator{identity: expectedIdentity}, nil),
		func(c *gin.Context) {
			capturedIdentity, _ = IdentityFromContext(c.Request.Context())
			c.Status(http.StatusOK)
		},
	)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer valid")
	router.ServeHTTP(rec, req)

	if capturedIdentity == nil {
		t.Fatal("expected identity in context, got nil")
	}
	if capturedIdentity.Role != domain.RoleMember {
		t.Errorf("expected member role, got %s", capturedIdentity.Role)
	}
}

func TestGinRequireAuth_UnexpectedError_Returns500(t *testing.T) {
	rec := runMiddleware(GinRequireAuth(&mockAuthenticator{err: errInternal}, nil), httptest.NewRequest("GET", "/test", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// --- GinAuthorize ---

func requestWithIdentityGin(identity *domain.Identity) *http.Request {
	req := httptest.NewRequest("GET", "/test", nil)
	return req.WithContext(contextWithIdentity(req.Context(), identity))
}

func TestGinAuthorize_NoIdentityInContext_Returns500(t *testing.T) {
	rec := runMiddleware(
		GinAuthorize(&mockAuthorizer{}, auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbRead}),
		httptest.NewRequest("GET", "/test", nil),
	)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when identity missing, got %d", rec.Code)
	}
}

func TestGinAuthorize_Forbidden_Returns403(t *testing.T) {
	rec := runMiddleware(
		GinAuthorize(&mockAuthorizer{err: api.ErrForbidden}, auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbWrite}),
		requestWithIdentityGin(&domain.Identity{Role: domain.RoleViewer}),
	)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestGinAuthorize_Allowed_CallsNext(t *testing.T) {
	var nextCalled bool

	rec := httptest.NewRecorder()
	router := gin.New()
	router.GET("/test",
		GinAuthorize(&mockAuthorizer{err: nil}, auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbRead}),
		func(c *gin.Context) {
			nextCalled = true
			c.Status(http.StatusOK)
		},
	)
	router.ServeHTTP(rec, requestWithIdentityGin(&domain.Identity{Role: domain.RoleMember}))

	if !nextCalled {
		t.Error("expected next to be called when authorized")
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

// errInternal is a non-sentinel error for testing the 500 path.
var errInternal = errInternalType{}

type errInternalType struct{}

func (errInternalType) Error() string { return "internal error" }
