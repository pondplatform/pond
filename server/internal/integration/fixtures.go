//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/serviceconfig"
	shared "github.com/pondplatform/pond/shared/server/api"
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
		Deployment: &serviceconfig.DeploymentSpec{
			Replicas: serviceconfig.Ptr(int32(1)),
		},
		Service: &serviceconfig.ServiceSpec{
			Port: serviceconfig.Ptr(int32(8080)),
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
func SubmitRequest(scenario ScenarioResult, cfg serviceconfig.ServiceConfig, imageTag string) shared.SubmitRequest {
	return shared.SubmitRequest{
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

// ServiceConfigWithDeps returns a ServiceConfig declaring the given
// dependencies, keyed by name with the dependency type as value.
func ServiceConfigWithDeps(name string, deps map[string]string) serviceconfig.ServiceConfig {
	cfg := MinimalServiceConfig(name)
	cfg.Dependencies = make(map[string]serviceconfig.DependencyDeclaration, len(deps))
	for depName, depType := range deps {
		cfg.Dependencies[depName] = serviceconfig.DependencyDeclaration{Type: depType}
	}
	return cfg
}

// WithConfigValues adds an app.yaml config file carrying values, so template
// placeholders in them are rendered when helm values are generated.
func WithConfigValues(cfg serviceconfig.ServiceConfig, values map[string]any) serviceconfig.ServiceConfig {
	cfg.Configs = map[string]serviceconfig.ConfigFileSpec{
		"app.yaml": {Format: "yaml", MountDir: "/etc/app", Values: values},
	}
	return cfg
}

// PostgresOutput is a tofu.apply output for a postgres dependency. It
// deliberately has no username: the postgres spec declares that field, so
// {{postgres.username}} passes up-front validation and fails only at render
// time, after the dependency has succeeded.
var PostgresOutput = map[string]any{
	"host":     "db.test.local",
	"port":     5432,
	"database": "testdb",
}

// PostgresUserConfig carries every postgres output field, as an unmanaged
// dependency must.
var PostgresUserConfig = map[string]any{
	"host":     "db.example.com",
	"port":     5432,
	"username": "app",
	"password": "app-password",
	"database": "app",
}
