package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

const testSecret = "test-secret-key"

func makeJWT(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign test token: %v", err)
	}
	return signed
}

func requestWithBearer(token string) *http.Request {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestJWTAuthenticator_NoToken(t *testing.T) {
	a := NewJWTAuthenticator([]byte(testSecret))
	r, _ := http.NewRequest("GET", "/", nil)
	_, err := a.Authenticate(context.Background(), r)
	if err != api.ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestJWTAuthenticator_InvalidToken(t *testing.T) {
	a := NewJWTAuthenticator([]byte(testSecret))
	r := requestWithBearer("not.a.jwt")
	_, err := a.Authenticate(context.Background(), r)
	if err != api.ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized for malformed JWT, got %v", err)
	}
}

func TestJWTAuthenticator_WrongSecret(t *testing.T) {
	raw := makeJWT(t, "other-secret", jwt.MapClaims{"role": "member"})
	a := NewJWTAuthenticator([]byte(testSecret))
	_, err := a.Authenticate(context.Background(), requestWithBearer(raw))
	if err != api.ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized for wrong secret, got %v", err)
	}
}

func TestJWTAuthenticator_AdminClaims(t *testing.T) {
	raw := makeJWT(t, testSecret, jwt.MapClaims{
		"is_admin":    true,
		"description": "CI key",
	})
	a := NewJWTAuthenticator([]byte(testSecret))
	identity, err := a.Authenticate(context.Background(), requestWithBearer(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity.Role != domain.RoleAdmin {
		t.Errorf("expected admin role, got %s", identity.Role)
	}
	if !identity.IsAdminKey {
		t.Error("expected IsAdminKey=true for admin JWT")
	}
	if identity.Description != "CI key" {
		t.Errorf("expected description 'CI key', got %q", identity.Description)
	}
}

func TestJWTAuthenticator_MemberRole(t *testing.T) {
	raw := makeJWT(t, testSecret, jwt.MapClaims{"role": "member"})
	a := NewJWTAuthenticator([]byte(testSecret))
	identity, err := a.Authenticate(context.Background(), requestWithBearer(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity.Role != domain.RoleMember {
		t.Errorf("expected member, got %s", identity.Role)
	}
	if identity.IsAdminKey {
		t.Error("expected IsAdminKey=false for role-scoped token")
	}
}

func TestJWTAuthenticator_ViewerRole(t *testing.T) {
	raw := makeJWT(t, testSecret, jwt.MapClaims{"role": "viewer"})
	a := NewJWTAuthenticator([]byte(testSecret))
	identity, err := a.Authenticate(context.Background(), requestWithBearer(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity.Role != domain.RoleViewer {
		t.Errorf("expected viewer, got %s", identity.Role)
	}
}

func TestJWTAuthenticator_AdminRoleViaRoleClaim(t *testing.T) {
	raw := makeJWT(t, testSecret, jwt.MapClaims{"role": "admin"})
	a := NewJWTAuthenticator([]byte(testSecret))
	identity, err := a.Authenticate(context.Background(), requestWithBearer(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity.Role != domain.RoleAdmin {
		t.Errorf("expected admin, got %s", identity.Role)
	}
}

func TestJWTAuthenticator_UnknownRoleRejected(t *testing.T) {
	raw := makeJWT(t, testSecret, jwt.MapClaims{"role": "superuser"})
	a := NewJWTAuthenticator([]byte(testSecret))
	_, err := a.Authenticate(context.Background(), requestWithBearer(raw))
	if err != api.ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized for unknown role, got %v", err)
	}
}

func TestJWTAuthenticator_MissingRoleClaimRejected(t *testing.T) {
	raw := makeJWT(t, testSecret, jwt.MapClaims{"description": "no role"})
	a := NewJWTAuthenticator([]byte(testSecret))
	_, err := a.Authenticate(context.Background(), requestWithBearer(raw))
	if err != api.ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized when role claim absent, got %v", err)
	}
}

func TestJWTAuthenticator_DescriptionOptional(t *testing.T) {
	raw := makeJWT(t, testSecret, jwt.MapClaims{"role": "viewer"})
	a := NewJWTAuthenticator([]byte(testSecret))
	identity, err := a.Authenticate(context.Background(), requestWithBearer(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity.Description != "" {
		t.Errorf("expected empty description, got %q", identity.Description)
	}
}

func TestJWTAuthenticator_ExpiredTokenStillAccepted(t *testing.T) {
	// JWTAuthenticator uses jwt.WithoutClaimsValidation(), so expiry is ignored.
	raw := makeJWT(t, testSecret, jwt.MapClaims{
		"role": "member",
		"exp":  time.Now().Add(-time.Hour).Unix(),
	})
	a := NewJWTAuthenticator([]byte(testSecret))
	_, err := a.Authenticate(context.Background(), requestWithBearer(raw))
	if err != nil {
		t.Errorf("expected expired token to be accepted (no claims validation), got %v", err)
	}
}
