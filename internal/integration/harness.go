//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/lib/pq"
	"github.com/pondplatform/pond/internal/cli/client"
	"github.com/pondplatform/pond/internal/common/config"
	"github.com/pondplatform/pond/internal/server/api"
	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/events"
	"github.com/pondplatform/pond/internal/server/helmgen"
	"github.com/pondplatform/pond/internal/server/service"
	"github.com/pondplatform/pond/internal/server/store"
)

// TestHarness owns a running server, its database connection, and test URLs.
type TestHarness struct {
	DB      *sql.DB
	BaseURL string // "http://127.0.0.1:<port>"
	WsAddr  string // "127.0.0.1:<port>" (without ws:// prefix, for agent connection)

	server *httptest.Server
	cancel context.CancelFunc
}

// NewTestHarness creates a new test harness with a running server.
// The server uses the database at the given connection string.
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

	ctx, cancel := context.WithCancel(context.Background())

	// Wire up the server components (mirrors internal/server/app.go)
	deploymentInfoStore := store.NewDeploymentInfoStore(db)
	envStore := store.NewEnvironmentStore(db)
	serviceStore := store.NewServiceStore(db)
	clusterStore := store.NewClusterStore(db)

	tx := newTestTransactor(db)
	specRegistry := dependency.NewSpecRegistry()
	bus := events.NewMemoryBus()

	depSvc := service.NewDependencyService(specRegistry)
	helmGenerator := helmgen.NewGenerator()
	tmplRenderer := config.NewTemplateRenderer()
	deploySvc := service.NewDeploymentService(
		deploymentInfoStore,
		serviceStore,
		envStore,
		depSvc,
		helmGenerator,
		tmplRenderer,
		tx,
		bus,
	)

	// Start the deployment service event loop
	go deploySvc.Start(ctx)

	// Create the agent handler and router
	agentHandler := api.NewAgentHandler(clusterStore, bus)
	router := api.NewRouter(deploySvc, serviceStore, envStore, agentHandler)

	// Start the test server
	server := httptest.NewServer(router)

	// Extract host:port for WebSocket connections
	wsAddr := strings.TrimPrefix(server.URL, "http://")

	h := &TestHarness{
		DB:      db,
		BaseURL: server.URL,
		WsAddr:  wsAddr,
		server:  server,
		cancel:  cancel,
	}

	t.Cleanup(h.Cleanup)

	return h
}

// Client returns a ServerClient configured to talk to this harness's server.
func (h *TestHarness) Client() client.ServerClient {
	return client.NewHTTPClient(h.BaseURL)
}

// Cleanup stops the server and closes the database connection.
func (h *TestHarness) Cleanup() {
	h.cancel()
	h.server.Close()
	h.DB.Close()
}

// testTransactor implements service.Transactor for tests.
type testTransactor struct {
	db *sql.DB
}

func newTestTransactor(db *sql.DB) service.Transactor {
	return &testTransactor{db: db}
}

func (t *testTransactor) RunInTx(ctx context.Context, fn func(ctx context.Context, tx service.TxRepos) error) error {
	sqlTx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	repos := service.TxRepos{
		DeploymentInfo: store.NewDeploymentInfoStore(sqlTx),
	}
	if err := fn(ctx, repos); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	return sqlTx.Commit()
}

// RunSchema applies the database schema from db/schema.sql.
func RunSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	// Find the schema file relative to the project root
	schemaPath := findSchemaFile(t)

	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema file: %v", err)
	}

	_, err = db.Exec(string(schema))
	if err != nil {
		t.Fatalf("execute schema: %v", err)
	}
}

// findSchemaFile locates db/schema.sql by walking up from the current directory.
func findSchemaFile(t *testing.T) string {
	t.Helper()

	// Start from the current working directory
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	// Walk up looking for db/schema.sql
	for {
		schemaPath := filepath.Join(dir, "db", "schema.sql")
		if _, err := os.Stat(schemaPath); err == nil {
			return schemaPath
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	t.Fatal("could not find db/schema.sql")
	return ""
}
