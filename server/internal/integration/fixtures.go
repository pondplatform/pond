//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"github.com/pondplatform/pond/server/internal/service"
)

// ScenarioResult contains the IDs and credentials created by BuildScenario.
type ScenarioResult struct {
	ClusterID  uuid.UUID
	ProjectID  uuid.UUID
	EnvID      uuid.UUID
	AgentToken string // plaintext cluster agent token
}

// BuildScenario creates a cluster, project, and environment via HTTP using the harness admin token.
// Services are created implicitly on first deployment (CreateIfNotExists: true).
func BuildScenario(ctx context.Context, t *testing.T, h *TestHarness) ScenarioResult {
	t.Helper()

	sc := newScenarioClient(h.BaseURL, h.AdminToken)
	id := uuid.New().String()[:8]

	cluster, err := sc.createCluster(ctx, fmt.Sprintf("test-cluster-%s", id))
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	project, err := sc.createProject(ctx, fmt.Sprintf("test-project-%s", id))
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	env, err := sc.createEnvironment(ctx, project.ID, "test-env", "test-namespace", cluster.ID)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}

	return ScenarioResult{
		ClusterID:  cluster.ID,
		ProjectID:  project.ID,
		EnvID:      env.ID,
		AgentToken: cluster.AgentToken,
	}
}

// MinimalServiceConfig returns a ServiceConfig with no dependencies.
func MinimalServiceConfig(name string) serviceconfig.ServiceConfig {
	return serviceconfig.ServiceConfig{
		Version: 1,
		Name:    name,
		Image:   "ghcr.io/example/" + name,
		Service: &serviceconfig.ServiceSpec{
			Port:     serviceconfig.Ptr(int32(8080)),
			Replicas: serviceconfig.Ptr(int32(1)),
		},
	}
}

// ServiceConfigWithDep returns a ServiceConfig with one managed postgres dependency.
func ServiceConfigWithDep(name string) serviceconfig.ServiceConfig {
	cfg := MinimalServiceConfig(name)
	cfg.Dependencies = map[string]serviceconfig.DependencyDeclaration{
		"postgres": {
			Type: "postgres",
		},
	}
	return cfg
}

// SubmitRequest builds a SubmitRequest from scenario data and a service config.
func SubmitRequest(scenario ScenarioResult, cfg serviceconfig.ServiceConfig, imageTag string) service.SubmitRequest {
	return service.SubmitRequest{
		ProjectID:       scenario.ProjectID,
		EnvironmentName: "test-env",
		OverridableConfig: serviceconfig.OverridableConfig{
			ServiceConfig: cfg,
		},
		ImageTag:          imageTag,
		TriggeredBy:       "integration-test",
		CreateIfNotExists: true,
	}
}
