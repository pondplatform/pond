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
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/service"
)

const testJWTSecret = "test-secret-for-integration-tests-only"

// TestHarness owns a running server, its database connection, and test URLs.
type TestHarness struct {
	DB         *sql.DB
	BaseURL    string // "http://127.0.0.1:<port>"
	WsAddr     string // "127.0.0.1:<port>" (without ws:// prefix, for agent connection)
	AdminToken string // JWT admin token for this harness

	timing Timing
	server *httptest.Server
	cancel context.CancelFunc
}

// Timing holds the liveness deadlines and retry settings of the deployment
// service's sweep. The zero value means the service defaults.
type Timing struct {
	QueueTimeout      time.Duration
	AckTimeout        time.Duration
	CommandTimeout    time.Duration
	Grace             time.Duration
	OrphanTimeout     time.Duration
	SweepInterval     time.Duration
	HeartbeatInterval time.Duration
	MaxAttempts       int
}

// FastTiming shrinks every deadline so tests about recovery and timeouts
// finish in seconds.
var FastTiming = Timing{
	QueueTimeout:      2 * time.Second,
	AckTimeout:        1 * time.Second,
	CommandTimeout:    1 * time.Second,
	Grace:             500 * time.Millisecond,
	OrphanTimeout:     3 * time.Second,
	SweepInterval:     200 * time.Millisecond,
	HeartbeatInterval: 300 * time.Millisecond,
	MaxAttempts:       3,
}

// NewTestHarness creates a new test harness with a running server.
// The server uses the database at the given connection string and the given
// RabbitMQ AMQP URL.
func NewTestHarness(t *testing.T, connStr string, amqpURL string) *TestHarness {
	t.Helper()
	return NewTestHarnessWithTiming(t, connStr, amqpURL, Timing{})
}

// NewTestHarnessWithTiming is NewTestHarness with the given sweep timing.
// The sweeper uses SweepInterval, CommandTimeout and OrphanTimeout (as its
// stall timeout); the other fields wait for the dispatch rework.
func NewTestHarnessWithTiming(t *testing.T, connStr string, amqpURL string, timing Timing) *TestHarness {
	t.Helper()

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("ping db: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	topics, rmq, err := events.NewRabbitMQTopics(amqpURL, log.WithGroup("rabbitmq"))
	if err != nil {
		cancel()
		db.Close()
		t.Fatalf("new rabbitmq topics: %v", err)
	}
	t.Cleanup(rmq.Close)

	router, background := server.Build(db, topics, server.Config{
		JWTSecret: testJWTSecret,
		Sweep: service.SweepConfig{
			Interval:       timing.SweepInterval,
			CommandTimeout: timing.CommandTimeout,
			StallTimeout:   timing.OrphanTimeout,
		},
	}, log)
	go background(ctx)

	// Start the test server
	ts := httptest.NewServer(router)

	// Extract host:port for WebSocket connections
	wsAddr := strings.TrimPrefix(ts.URL, "http://")

	adminToken := mintAdminToken(t)

	h := &TestHarness{
		DB:         db,
		BaseURL:    ts.URL,
		WsAddr:     wsAddr,
		AdminToken: adminToken,
		timing:     timing,
		server:     ts,
		cancel:     cancel,
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
	h.cancel()
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
