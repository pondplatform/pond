package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"

	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

// stubAuthenticator is an Authenticator that returns a fixed identity/error.
type stubAuthenticator struct {
	identity *domain.Identity
	err      error
	called   bool
}

func (s *stubAuthenticator) Authenticate(_ context.Context, _ *http.Request) (*domain.Identity, error) {
	s.called = true
	return s.identity, s.err
}

func TestAdminKeyAuthenticator_MatchingKeyReturnsAdminIdentity(t *testing.T) {
	inner := &stubAuthenticator{}
	a := NewAdminKeyAuthenticator("secret-key", inner)

	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer secret-key")

	identity, err := a.Authenticate(context.Background(), r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity.Role != domain.RoleAdmin {
		t.Errorf("expected admin role, got %s", identity.Role)
	}
	if !identity.IsAdminKey {
		t.Error("expected IsAdminKey=true")
	}
	if inner.called {
		t.Error("inner authenticator should not be called when admin key matches")
	}
}

func TestAdminKeyAuthenticator_NonMatchingTokenFallsThrough(t *testing.T) {
	expectedIdentity := &domain.Identity{Role: domain.RoleMember}
	inner := &stubAuthenticator{identity: expectedIdentity}
	a := NewAdminKeyAuthenticator("secret-key", inner)

	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer wrong-key")

	identity, err := a.Authenticate(context.Background(), r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity != expectedIdentity {
		t.Error("expected inner authenticator's identity to be returned")
	}
	if !inner.called {
		t.Error("expected inner authenticator to be called")
	}
}

func TestAdminKeyAuthenticator_EmptyAdminKeyAlwaysFallsThrough(t *testing.T) {
	expectedIdentity := &domain.Identity{Role: domain.RoleViewer}
	inner := &stubAuthenticator{identity: expectedIdentity}
	a := NewAdminKeyAuthenticator("", inner)

	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer anything")

	identity, err := a.Authenticate(context.Background(), r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity != expectedIdentity {
		t.Error("expected passthrough when admin key is empty")
	}
}

func TestAdminKeyAuthenticator_InnerErrorPropagates(t *testing.T) {
	inner := &stubAuthenticator{err: api.ErrUnauthorized}
	a := NewAdminKeyAuthenticator("secret-key", inner)

	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer not-the-key")

	_, err := a.Authenticate(context.Background(), r)
	if !errors.Is(err, api.ErrUnauthorized) {
		t.Errorf("expected inner error to propagate, got %v", err)
	}
}

func TestAdminKeyAuthenticator_NoTokenFallsThrough(t *testing.T) {
	inner := &stubAuthenticator{err: api.ErrUnauthorized}
	a := NewAdminKeyAuthenticator("secret-key", inner)

	r, _ := http.NewRequest("GET", "/", nil) // no Authorization header

	_, err := a.Authenticate(context.Background(), r)
	if !errors.Is(err, api.ErrUnauthorized) {
		t.Errorf("expected inner error, got %v", err)
	}
	if !inner.called {
		t.Error("expected inner authenticator to be called when no token present")
	}
}
