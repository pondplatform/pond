//go:build integration

package integration

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/service"
)

// SeedResult contains the IDs and credentials created by SeedFixtures.
type SeedResult struct {
	OrgID      uuid.UUID
	ClusterID  uuid.UUID
	ProjectID  uuid.UUID
	EnvID      uuid.UUID
	ServiceID  uuid.UUID
	AgentToken string // plaintext token; SHA-256 hash is stored in DB
}

// SeedFixtures inserts a minimal set of rows sufficient for a deployment test.
// Each call generates fresh UUIDs for test isolation.
func SeedFixtures(t *testing.T, db *sql.DB) SeedResult {
	t.Helper()

	seed := SeedResult{
		OrgID:      uuid.New(),
		ClusterID:  uuid.New(),
		ProjectID:  uuid.New(),
		EnvID:      uuid.New(),
		ServiceID:  uuid.New(),
		AgentToken: uuid.New().String(), // random plaintext token
	}

	tokenHash := sha256hex(seed.AgentToken)

	// Insert organization
	_, err := db.Exec(`INSERT INTO organizations (id, name) VALUES ($1, $2)`,
		seed.OrgID, "test-org-"+seed.OrgID.String()[:8])
	if err != nil {
		t.Fatalf("insert organization: %v", err)
	}

	// Insert cluster with agent token hash
	_, err = db.Exec(`INSERT INTO clusters (id, organization_id, name, agent_token_hash) VALUES ($1, $2, $3, $4)`,
		seed.ClusterID, seed.OrgID, "test-cluster", tokenHash)
	if err != nil {
		t.Fatalf("insert cluster: %v", err)
	}

	// Insert project
	_, err = db.Exec(`INSERT INTO projects (id, organization_id, name) VALUES ($1, $2, $3)`,
		seed.ProjectID, seed.OrgID, "test-project")
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}

	// Insert environment
	_, err = db.Exec(`INSERT INTO environments (id, project_id, name, namespace, cluster_id) VALUES ($1, $2, $3, $4, $5)`,
		seed.EnvID, seed.ProjectID, "test-env", "test-namespace", seed.ClusterID)
	if err != nil {
		t.Fatalf("insert environment: %v", err)
	}

	// Insert service
	_, err = db.Exec(`INSERT INTO services (id, project_id, name) VALUES ($1, $2, $3)`,
		seed.ServiceID, seed.ProjectID, "test-service")
	if err != nil {
		t.Fatalf("insert service: %v", err)
	}

	return seed
}

// sha256hex returns the hex-encoded SHA-256 hash of the input string.
func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// MinimalServiceConfig returns a ServiceConfig with no dependencies.
func MinimalServiceConfig(name string) domain.ServiceConfig {
	return domain.ServiceConfig{
		Version: 1,
		Name:    name,
		Image:   "ghcr.io/example/" + name,
		Service: domain.ServiceSpec{
			Port:     8080,
			Replicas: 1,
		},
	}
}

// ServiceConfigWithDep returns a ServiceConfig with one managed postgres dependency.
func ServiceConfigWithDep(name string) domain.ServiceConfig {
	cfg := MinimalServiceConfig(name)
	cfg.Dependencies = map[string]domain.DependencyDeclaration{
		"postgres": {
			Type: "postgres",
		},
	}
	return cfg
}

// SubmitRequest builds a SubmitRequest from seed data and a service config.
func SubmitRequest(seed SeedResult, cfg domain.ServiceConfig, imageTag string) service.SubmitRequest {
	return service.SubmitRequest{
		ProjectID:     seed.ProjectID,
		EnvironmentID: seed.EnvID,
		ServiceConfig: cfg,
		ImageTag:      imageTag,
		TriggeredBy:   "integration-test",
	}
}
