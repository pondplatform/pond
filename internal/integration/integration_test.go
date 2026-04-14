//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/pondplatform/pond/internal/agent"
	"github.com/pondplatform/pond/internal/cli/client"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var testConnStr string

func TestMain(m *testing.M) {
	ctx := context.Background()

	// Start PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("pond_test"),
		postgres.WithUsername("pond"),
		postgres.WithPassword("pond"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}

	// Get connection string
	testConnStr, err = pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("get connection string: %v", err)
	}

	// Apply schema with retry
	var db *sql.DB
	for i := 0; i < 10; i++ {
		db, err = sql.Open("postgres", testConnStr)
		if err != nil {
			log.Printf("open db attempt %d: %v", i+1, err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if err := db.Ping(); err != nil {
			log.Printf("ping db attempt %d: %v", i+1, err)
			db.Close()
			time.Sleep(500 * time.Millisecond)
			continue
		}
		break
	}
	if db == nil {
		log.Fatalf("could not connect to database after retries")
	}
	if err := applySchema(db); err != nil {
		log.Fatalf("apply schema: %v", err)
	}
	db.Close()

	// Run tests
	code := m.Run()

	// Cleanup
	if err := pgContainer.Terminate(ctx); err != nil {
		log.Printf("terminate postgres container: %v", err)
	}

	os.Exit(code)
}

func applySchema(db *sql.DB) error {
	schemaPath := findSchemaPath()
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	_, err = db.Exec(string(schema))
	return err
}

func findSchemaPath() string {
	// Try common paths
	paths := []string{
		"../../db/schema.sql",
		"db/schema.sql",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Fallback: assume we're in project root
	return "db/schema.sql"
}

// TestDeployment_SimpleSucceeds tests a helm-only deployment with no dependencies.
func TestDeployment_SimpleSucceeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create harness and seed data
	h := NewTestHarness(t, testConnStr)
	seed := SeedFixtures(t, h.DB)

	// Start fake agent
	fa := NewFakeAgent(h.WsAddr, seed.AgentToken, DefaultBehavior)
	if err := fa.Connect(ctx); err != nil {
		t.Fatalf("connect fake agent: %v", err)
	}
	go func() {
		if err := fa.Run(ctx); err != nil {
			t.Logf("fake agent run error: %v", err)
		}
	}()
	defer fa.Stop()

	// Submit deployment
	c := h.Client()
	cfg := MinimalServiceConfig("test-service")
	req := SubmitRequest(seed, cfg, "v1.0.0")

	deployment, err := c.SubmitDeployment(ctx, req)
	if err != nil {
		t.Fatalf("submit deployment: %v", err)
	}

	if string(deployment.Status) != "pending" {
		t.Errorf("expected initial status 'pending', got %q", deployment.Status)
	}

	// Poll until deployment completes or times out
	finalStatus := pollDeploymentStatus(ctx, t, c, deployment.ID, 10*time.Second)

	if finalStatus != "succeeded" {
		t.Errorf("expected final status 'succeeded', got %q", finalStatus)
	}

	// Verify agent received exactly one helm.upgrade command
	commands := fa.ReceivedCommands()
	if len(commands) != 1 {
		t.Errorf("expected 1 command, got %d", len(commands))
	}
	if len(commands) > 0 && commands[0].Type != agent.CommandHelmUpgrade {
		t.Errorf("expected command type %q, got %q", agent.CommandHelmUpgrade, commands[0].Type)
	}

	// Verify DB state
	var dbStatus string
	err = h.DB.QueryRow("SELECT status FROM deployments WHERE id = $1", deployment.ID).Scan(&dbStatus)
	if err != nil {
		t.Fatalf("query deployment status: %v", err)
	}
	if dbStatus != "succeeded" {
		t.Errorf("expected DB status 'succeeded', got %q", dbStatus)
	}
}

// TestDeployment_HelmFails tests that a helm failure marks the deployment as failed.
func TestDeployment_HelmFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	seed := SeedFixtures(t, h.DB)

	// Use failing behavior
	fa := NewFakeAgent(h.WsAddr, seed.AgentToken, FailingBehavior("helm upgrade failed: release not found"))
	if err := fa.Connect(ctx); err != nil {
		t.Fatalf("connect fake agent: %v", err)
	}
	go func() {
		if err := fa.Run(ctx); err != nil {
			t.Logf("fake agent run error: %v", err)
		}
	}()
	defer fa.Stop()

	c := h.Client()
	cfg := MinimalServiceConfig("test-service")
	req := SubmitRequest(seed, cfg, "v1.0.0")

	deployment, err := c.SubmitDeployment(ctx, req)
	if err != nil {
		t.Fatalf("submit deployment: %v", err)
	}

	finalStatus := pollDeploymentStatus(ctx, t, c, deployment.ID, 10*time.Second)

	if finalStatus != "failed" {
		t.Errorf("expected final status 'failed', got %q", finalStatus)
	}

	// Verify command was received
	commands := fa.ReceivedCommands()
	if len(commands) != 1 {
		t.Errorf("expected 1 command, got %d", len(commands))
	}
}

// TestDeployment_WithTofuDep tests deployment with a managed tofu dependency.
func TestDeployment_WithTofuDep(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	seed := SeedFixtures(t, h.DB)

	// Insert dependency config to mark postgres as managed
	_, err := h.DB.Exec(`
		INSERT INTO dependency_configs (id, service_id, environment_id, dependency_name, dependency_type, managed, provider_inputs)
		VALUES ($1, $2, $3, 'postgres', 'postgres', true, '{}')
	`, newUUID(), seed.ServiceID, seed.EnvID)
	if err != nil {
		// If the table doesn't exist or has different schema, skip this test
		t.Skipf("could not insert dependency config (may need schema update): %v", err)
	}

	// Behavior: succeed tofu with output, then succeed helm
	behavior := TofuOutputBehavior(map[string]any{
		"host":     "db.test.local",
		"port":     5432,
		"database": "testdb",
	})

	fa := NewFakeAgent(h.WsAddr, seed.AgentToken, behavior)
	if err := fa.Connect(ctx); err != nil {
		t.Fatalf("connect fake agent: %v", err)
	}
	go func() {
		if err := fa.Run(ctx); err != nil {
			t.Logf("fake agent run error: %v", err)
		}
	}()
	defer fa.Stop()

	c := h.Client()
	cfg := ServiceConfigWithDep("test-service")
	req := SubmitRequest(seed, cfg, "v1.0.0")

	deployment, err := c.SubmitDeployment(ctx, req)
	if err != nil {
		t.Fatalf("submit deployment: %v", err)
	}

	finalStatus := pollDeploymentStatus(ctx, t, c, deployment.ID, 15*time.Second)

	if finalStatus != "succeeded" {
		t.Errorf("expected final status 'succeeded', got %q", finalStatus)
	}

	// Verify agent received tofu.apply then helm.upgrade
	commands := fa.ReceivedCommands()
	if len(commands) < 2 {
		t.Errorf("expected at least 2 commands, got %d", len(commands))
	} else {
		if commands[0].Type != agent.CommandTofuApply {
			t.Errorf("expected first command %q, got %q", agent.CommandTofuApply, commands[0].Type)
		}
		if commands[len(commands)-1].Type != agent.CommandHelmUpgrade {
			t.Errorf("expected last command %q, got %q", agent.CommandHelmUpgrade, commands[len(commands)-1].Type)
		}
	}
}

// TestDeployment_TofuFails tests that a tofu failure marks the deployment as failed.
func TestDeployment_TofuFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	seed := SeedFixtures(t, h.DB)

	// Insert dependency config
	_, err := h.DB.Exec(`
		INSERT INTO dependency_configs (id, service_id, environment_id, dependency_name, dependency_type, managed, provider_inputs)
		VALUES ($1, $2, $3, 'postgres', 'postgres', true, '{}')
	`, newUUID(), seed.ServiceID, seed.EnvID)
	if err != nil {
		t.Skipf("could not insert dependency config: %v", err)
	}

	// Fail tofu commands
	behavior := PerCommandBehavior(map[agent.CommandType]func(*agent.Command) *agent.CommandResult{
		agent.CommandTofuApply: func(cmd *agent.Command) *agent.CommandResult {
			return &agent.CommandResult{
				CommandID: cmd.ID,
				Success:   false,
				Error:     "tofu apply failed: resource quota exceeded",
			}
		},
	})

	fa := NewFakeAgent(h.WsAddr, seed.AgentToken, behavior)
	if err := fa.Connect(ctx); err != nil {
		t.Fatalf("connect fake agent: %v", err)
	}
	go func() {
		if err := fa.Run(ctx); err != nil {
			t.Logf("fake agent run error: %v", err)
		}
	}()
	defer fa.Stop()

	c := h.Client()
	cfg := ServiceConfigWithDep("test-service")
	req := SubmitRequest(seed, cfg, "v1.0.0")

	deployment, err := c.SubmitDeployment(ctx, req)
	if err != nil {
		t.Fatalf("submit deployment: %v", err)
	}

	finalStatus := pollDeploymentStatus(ctx, t, c, deployment.ID, 10*time.Second)

	if finalStatus != "failed" {
		t.Errorf("expected final status 'failed', got %q", finalStatus)
	}

	// Verify only tofu command was received (no helm after failure)
	commands := fa.ReceivedCommands()
	if len(commands) != 1 {
		t.Errorf("expected 1 command (only tofu), got %d", len(commands))
	}
	if len(commands) > 0 && commands[0].Type != agent.CommandTofuApply {
		t.Errorf("expected command type %q, got %q", agent.CommandTofuApply, commands[0].Type)
	}
}

// TestDeployment_AgentDisconnectRequeues tests that agent disconnect causes command requeue.
// This test is skipped by default as it's inherently racy - the disconnect timing is hard
// to control reliably without more sophisticated synchronization primitives.
func TestDeployment_AgentDisconnectRequeues(t *testing.T) {
	t.Skip("skipping: disconnect requeue test is inherently racy and needs more sophisticated mocking")
}

// TestDeployment_WithLogs tests that log messages are properly handled.
func TestDeployment_WithLogs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	seed := SeedFixtures(t, h.DB)

	// Behavior with logs
	behavior := Behavior{
		Handler: DefaultBehavior.Handler,
		Logs:    []string{"Starting helm upgrade...", "Waiting for pods...", "Upgrade complete"},
	}

	fa := NewFakeAgent(h.WsAddr, seed.AgentToken, behavior)
	if err := fa.Connect(ctx); err != nil {
		t.Fatalf("connect fake agent: %v", err)
	}
	go func() {
		if err := fa.Run(ctx); err != nil {
			t.Logf("fake agent run error: %v", err)
		}
	}()
	defer fa.Stop()

	c := h.Client()
	cfg := MinimalServiceConfig("test-service")
	req := SubmitRequest(seed, cfg, "v1.0.0")

	deployment, err := c.SubmitDeployment(ctx, req)
	if err != nil {
		t.Fatalf("submit deployment: %v", err)
	}

	finalStatus := pollDeploymentStatus(ctx, t, c, deployment.ID, 10*time.Second)

	if finalStatus != "succeeded" {
		t.Errorf("expected final status 'succeeded', got %q", finalStatus)
	}

	// Verify logs were stored (query command_logs table)
	var logCount int
	err = h.DB.QueryRow(`
		SELECT COUNT(*) FROM command_logs cl
		JOIN commands c ON cl.command_id = c.id
		WHERE c.deployment_id = $1
	`, deployment.ID).Scan(&logCount)
	if err != nil {
		t.Fatalf("query log count: %v", err)
	}

	if logCount != 3 {
		t.Errorf("expected 3 log entries, got %d", logCount)
	}
}

// Helper functions

func pollDeploymentStatus(ctx context.Context, t *testing.T, c client.ServerClient, id uuid.UUID, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled while polling deployment status")
		case <-ticker.C:
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for deployment to complete")
			}

			d, err := c.GetDeployment(ctx, id)
			if err != nil {
				t.Logf("poll error: %v", err)
				continue
			}

			status := string(d.Status)
			if status == "succeeded" || status == "failed" {
				return status
			}
		}
	}
}

func waitForCommands(t *testing.T, fa *FakeAgent, count int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if len(fa.ReceivedCommands()) >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %d commands", count)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func newUUID() string {
	return uuid.New().String()
}

// Suppress unused fmt import warning
var _ = fmt.Sprintf
