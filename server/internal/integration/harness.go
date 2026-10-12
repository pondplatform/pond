//go:build integration

package integration

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	_ "github.com/lib/pq"
	"github.com/pondplatform/pond/cli/client"
	server "github.com/pondplatform/pond/server/internal"
)

const testJWTSecret = "test-secret-for-integration-tests-only"

// TestHarness owns a running server, its database connection, and test URLs.
type TestHarness struct {
	DB         *sql.DB
	BaseURL    string // "http://127.0.0.1:<port>"
	WsAddr     string // "127.0.0.1:<port>" (without ws:// prefix, for agent connection)
	AdminToken string // JWT admin token for this harness

	server *httptest.Server
}

// NewTestHarness creates a new test harness with a running server that uses
// the database at the given connection string.
func NewTestHarness(t *testing.T, connStr string) *TestHarness {
	t.Helper()

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("ping db: %v", err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ts := httptest.NewServer(server.Build(db, server.Config{
		DatabaseURL:       connStr,
		JWTSecret:         testJWTSecret,
		StopCheckInterval: 50 * time.Millisecond,
	}, log))

	h := &TestHarness{
		DB:         db,
		BaseURL:    ts.URL,
		WsAddr:     strings.TrimPrefix(ts.URL, "http://"),
		AdminToken: mintAdminToken(t),
		server:     ts,
	}
	t.Cleanup(h.Cleanup)
	return h
}

// Client returns a ServerClient configured to talk to this harness's server.
// The client carries the harness admin token on every request.
func (h *TestHarness) Client() client.ServerClient {
	return client.NewHTTPClientWithToken(h.BaseURL, h.AdminToken)
}

// StartFakeAgent creates, connects, and starts a FakeAgent in a background goroutine.
// It registers t.Cleanup to stop the agent when the test ends.
func (h *TestHarness) StartFakeAgent(ctx context.Context, t *testing.T, token string, b Behavior) *FakeAgent {
	t.Helper()
	fa := NewFakeAgent(h.WsAddr, token, b)
	if err := fa.Connect(ctx); err != nil {
		t.Fatalf("start fake agent: %v", err)
	}
	go func() {
		if err := fa.Run(ctx); err != nil {
			t.Logf("fake agent run error: %v", err)
		}
	}()
	t.Cleanup(fa.Stop)
	return fa
}

// Cleanup stops the server and closes the database connection.
func (h *TestHarness) Cleanup() {
	h.server.Close()
	h.DB.Close()
}

// mintAdminToken mints a JWT admin token for tests.
func mintAdminToken(t *testing.T) string {
	t.Helper()

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"role":        "admin",
		"description": "test-harness",
	})
	signed, err := tok.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("sign bootstrap jwt: %v", err)
	}
	return signed
}
