package helmgen

import (
	"encoding/base64"
	"strings"
	"testing"

	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
)

func TestGenerator_Generate_ConfigFiles(t *testing.T) {
	g := NewGenerator()

	// Values are pre-rendered by the deployment service before calling Generate
	cfg := &serviceconfig.ServiceConfig{
		Name: "test-service",
		Configs: map[string]serviceconfig.ConfigFileSpec{
			"app.yaml": {
				Format:   "yaml",
				MountDir: "/etc/app",
				Values: map[string]any{
					"database_url": "postgres://admin:password123@db-host:5432/mydb",
					"debug":        true,
				},
			},
		},
	}

	env := &domain.Environment{
		DefaultIngressBaseHost: "example.com",
	}

	vals, err := g.Generate(cfg, env)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(vals.Configs) != 1 {
		t.Fatalf("Expected 1 config, got %d", len(vals.Configs))
	}

	config := vals.Configs[0]
	if config.MountLocation != "/etc/app/app.yaml" {
		t.Errorf("Unexpected mount location: %s", config.MountLocation)
	}

	decoded, err := base64.StdEncoding.DecodeString(config.Data)
	if err != nil {
		t.Fatalf("Failed to decode config data: %v", err)
	}

	expected := "database_url: postgres://admin:password123@db-host:5432/mydb\ndebug: true\n"
	if string(decoded) != expected {
		t.Errorf("Unexpected rendered config:\nExpected: %q\nGot:      %q", expected, string(decoded))
	}
}

func TestGenerator_Generate_GatewayHTTPRouteDerivesHostname(t *testing.T) {
	g := NewGenerator()

	cfg := &serviceconfig.ServiceConfig{
		Name: "web",
		Gateway: &serviceconfig.GatewayConfig{
			HTTP: &serviceconfig.GatewayHTTPConfig{
				Enabled: serviceconfig.Ptr(true),
			},
		},
	}

	env := &domain.Environment{
		DefaultIngressBaseHost: "pond.local",
	}

	vals, err := g.Generate(cfg, env)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if !vals.HTTPRoute.Enabled {
		t.Fatal("Expected httpRoute to be enabled")
	}

	if len(vals.HTTPRoute.Hostnames) != 1 || vals.HTTPRoute.Hostnames[0] != "web.pond.local" {
		t.Errorf("Unexpected hostnames: %v", vals.HTTPRoute.Hostnames)
	}

	// With no rules configured, a catch-all prefix route is supplied so the
	// chart's placeholder rule can never survive helm's list replacement.
	if len(vals.HTTPRoute.Rules) != 1 {
		t.Fatalf("Expected 1 defaulted rule, got %d", len(vals.HTTPRoute.Rules))
	}
	matches := vals.HTTPRoute.Rules[0].Matches
	if len(matches) != 1 || matches[0].Path == nil {
		t.Fatalf("Expected a single path match, got %+v", matches)
	}
	if *matches[0].Path.Type != "PathPrefix" || *matches[0].Path.Value != "/" {
		t.Errorf("Unexpected default path match: %+v", *matches[0].Path)
	}
}

func TestGenerator_Generate_GatewayHTTPRouteExplicitHostnameAndRules(t *testing.T) {
	g := NewGenerator()

	rules := []serviceconfig.HTTPRouteRule{{
		Matches: []serviceconfig.HTTPRouteMatch{{
			Path: &serviceconfig.HTTPPathMatch{
				Type:  serviceconfig.Ptr("Exact"),
				Value: serviceconfig.Ptr("/healthz"),
			},
		}},
	}}

	cfg := &serviceconfig.ServiceConfig{
		Name: "web",
		Gateway: &serviceconfig.GatewayConfig{
			HTTP: &serviceconfig.GatewayHTTPConfig{
				Enabled:  serviceconfig.Ptr(true),
				Hostname: serviceconfig.Ptr("api.example.com"),
				Rules:    rules,
			},
		},
	}

	env := &domain.Environment{
		DefaultIngressBaseHost: "pond.local",
	}

	vals, err := g.Generate(cfg, env)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(vals.HTTPRoute.Hostnames) != 1 || vals.HTTPRoute.Hostnames[0] != "api.example.com" {
		t.Errorf("Explicit hostname should win, got: %v", vals.HTTPRoute.Hostnames)
	}

	if len(vals.HTTPRoute.Rules) != 1 {
		t.Fatalf("Expected the configured rule to pass through, got %d", len(vals.HTTPRoute.Rules))
	}
	if *vals.HTTPRoute.Rules[0].Matches[0].Path.Value != "/healthz" {
		t.Errorf("Unexpected rule: %+v", vals.HTTPRoute.Rules[0])
	}
}

func TestGenerator_Generate_GatewayDisabledByDefault(t *testing.T) {
	g := NewGenerator()

	vals, err := g.Generate(&serviceconfig.ServiceConfig{Name: "web"}, &domain.Environment{
		DefaultIngressBaseHost: "pond.local",
	})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if vals.HTTPRoute.Enabled {
		t.Error("Expected httpRoute to be disabled when gateway is unset")
	}
	if len(vals.HTTPRoute.Rules) != 0 || len(vals.HTTPRoute.Hostnames) != 0 {
		t.Errorf("Expected no rules or hostnames when disabled: %+v", vals.HTTPRoute)
	}
}

// Regression guard: an unset replica count must be omitted from the values so
// the chart default of 1 applies, rather than pinning the deployment to zero.
func TestGenerator_Generate_OmitsUnsetReplicaCountAndPort(t *testing.T) {
	g := NewGenerator()

	vals, err := g.Generate(&serviceconfig.ServiceConfig{Name: "web"}, &domain.Environment{})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if vals.ReplicaCount != nil {
		t.Errorf("Expected replicaCount to be omitted, got %d", *vals.ReplicaCount)
	}
	if vals.Service.Port != nil {
		t.Errorf("Expected service port to be omitted, got %d", *vals.Service.Port)
	}

	out, err := yaml.Marshal(vals)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if strings.Contains(string(out), "replicaCount") {
		t.Errorf("replicaCount should not appear in the values file:\n%s", out)
	}
}

func TestGenerator_Generate_ReplicasFromDeployment(t *testing.T) {
	g := NewGenerator()

	cfg := &serviceconfig.ServiceConfig{
		Name: "web",
		Deployment: &serviceconfig.DeploymentSpec{
			Replicas: serviceconfig.Ptr(int32(3)),
		},
	}

	vals, err := g.Generate(cfg, &domain.Environment{})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if vals.ReplicaCount == nil || *vals.ReplicaCount != 3 {
		t.Errorf("Unexpected replicaCount: %v", vals.ReplicaCount)
	}
}
