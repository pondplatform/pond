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
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/pondplatform/pond/internal/cli/client"
	"github.com/pondplatform/pond/internal/common/config"
	"github.com/pondplatform/pond/internal/server/api"
	"github.com/pondplatform/pond/internal/server/auth"
	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/events"
	"github.com/pondplatform/pond/internal/server/helmgen"
	"github.com/pondplatform/pond/internal/server/service"
	"github.com/pondplatform/pond/internal/server/store"
)

const testJWTSecret = "test-secret-for-integration-tests-only"

// TestHarness owns a running server, its database connection, and test URLs.
type TestHarness struct {
	DB         *sql.DB
	BaseURL    string    // "http://127.0.0.1:<port>"
	WsAddr     string    // "127.0.0.1:<port>" (without ws:// prefix, for agent connection)
	OrgID      uuid.UUID // bootstrapped org for this harness
	AdminToken string    // JWT admin token for this harness

	server *httptest.Server
	cancel context.CancelFunc
}

// NewTestHarness creates a new test harness with a running server.
// The server uses the database at the given connection string and the given
// RabbitMQ AMQP URL.
func NewTestHarness(t *testing.T, connStr string, amqpURL string) *TestHarness {
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
	orgStore := store.NewOrganizationStore(db)
	projectStore := store.NewProjectStore(db)

	jwtSecret := []byte(testJWTSecret)
	authenticator := auth.NewJWTAuthenticator(jwtSecret)
	authorizer := auth.NewRoleAuthorizer()

	tx := newTestTransactor(db)
	specRegistry := dependency.NewSpecRegistry()
	bus, closeBus, err := events.NewRabbitMQBus(amqpURL)
	if err != nil {
		cancel()
		db.Close()
		t.Fatalf("new rabbitmq bus: %v", err)
	}
	t.Cleanup(closeBus)

	depSvc := service.NewDependencyService(specRegistry)
	helmGenerator := helmgen.NewGenerator()
	tmplRenderer := config.NewTemplateRenderer()
	resolver := config.NewResolver()
	deploySvc := service.NewDeploymentService(
		deploymentInfoStore,
		serviceStore,
		envStore,
		depSvc,
		helmGenerator,
		tmplRenderer,
		resolver,
		tx,
		bus,
	)

	// Start the deployment service event loop
	go deploySvc.Start(ctx)

	// Create the agent handler and router
	agentHandler := api.NewAgentHandler(clusterStore, bus)
	router := api.NewRouter(api.RouterDeps{
		DeploySvc:     deploySvc,
		Orgs:          orgStore,
		Projects:      projectStore,
		Envs:          envStore,
		Services:      serviceStore,
		Clusters:      clusterStore,
		JWTSecret:     jwtSecret,
		SpecRegistry:  specRegistry,
		AgentHandler:  agentHandler,
		Authenticator: authenticator,
		Authorizer:    authorizer,
	})

	// Start the test server
	server := httptest.NewServer(router)

	// Extract host:port for WebSocket connections
	wsAddr := strings.TrimPrefix(server.URL, "http://")

	// Bootstrap org and mint an admin JWT directly (chicken-and-egg: HTTP endpoints
	// for org creation require a token, but token creation requires an org that was
	// created by an admin).
	orgID, adminToken := bootstrapOrgAndToken(t, db)

	h := &TestHarness{
		DB:         db,
		BaseURL:    server.URL,
		WsAddr:     wsAddr,
		OrgID:      orgID,
		AdminToken: adminToken,
		server:     server,
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

// bootstrapOrgAndToken inserts a minimal org directly in the DB and mints an
// admin JWT. This bypasses the HTTP API to avoid the chicken-and-egg problem
// where every endpoint requires a token, but token creation requires an org
// that was created by an admin.
func bootstrapOrgAndToken(t *testing.T, db *sql.DB) (orgID uuid.UUID, adminJWT string) {
	t.Helper()

	orgID = uuid.New()
	now := time.Now().UTC()

	_, err := db.Exec(
		`INSERT INTO organizations (id, name, created_at) VALUES ($1, $2, $3)`,
		orgID, "test-org-"+orgID.String()[:8], now,
	)
	if err != nil {
		t.Fatalf("bootstrap org: %v", err)
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"org_id":      orgID.String(),
		"role":        "admin",
		"description": "test-harness",
	})
	signed, err := tok.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("sign bootstrap jwt: %v", err)
	}

	return orgID, signed
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
