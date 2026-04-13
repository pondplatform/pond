package helmgen_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/common/helmgen"
)

func minimalConfig() *domain.ServiceConfig {
	return &domain.ServiceConfig{
		Name:  "my-svc",
		Image: "my-image:latest",
		Service: domain.ServiceSpec{
			Port:     8080,
			Replicas: 2,
		},
	}
}

func testEnv() *domain.Environment {
	return &domain.Environment{
		Name:                   "prod",
		DefaultIngressBaseHost: "example.com",
	}
}

func TestGenerate_MinimalConfig(t *testing.T) {
	g := helmgen.NewGenerator()
	cfg := minimalConfig()

	vals, err := g.Generate(cfg, testEnv(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if vals.ReplicaCount != 2 {
		t.Errorf("expected ReplicaCount=2, got %d", vals.ReplicaCount)
	}
	if vals.Image.Repository != "my-image:latest" {
		t.Errorf("expected Image.Repository=%q, got %q", "my-image:latest", vals.Image.Repository)
	}
	if vals.Service.Port != 8080 {
		t.Errorf("expected Service.Port=8080, got %d", vals.Service.Port)
	}
	if vals.NameOverride != "my-svc" {
		t.Errorf("expected NameOverride=%q, got %q", "my-svc", vals.NameOverride)
	}
	if vals.Ingress.Enabled {
		t.Error("expected Ingress.Enabled=false for minimal config")
	}
	if vals.LivenessProbe != nil {
		t.Error("expected LivenessProbe=nil for minimal config")
	}
	if vals.ReadinessProbe != nil {
		t.Error("expected ReadinessProbe=nil for minimal config")
	}
	if vals.PodAnnotations != nil {
		t.Error("expected PodAnnotations=nil for minimal config")
	}
}

func TestGenerate_IngressEnabled_HostAndTLS(t *testing.T) {
	g := helmgen.NewGenerator()
	cfg := minimalConfig()
	cfg.Ingress.Enabled = true

	vals, err := g.Generate(cfg, testEnv(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !vals.Ingress.Enabled {
		t.Error("expected Ingress.Enabled=true")
	}
	if len(vals.Ingress.Hosts) != 1 {
		t.Fatalf("expected 1 ingress host, got %d", len(vals.Ingress.Hosts))
	}
	expectedHost := "my-svc.example.com"
	if vals.Ingress.Hosts[0].Host != expectedHost {
		t.Errorf("expected host=%q, got %q", expectedHost, vals.Ingress.Hosts[0].Host)
	}
	if len(vals.Ingress.TLS) != 1 {
		t.Fatalf("expected 1 TLS entry, got %d", len(vals.Ingress.TLS))
	}
	if vals.Ingress.TLS[0].SecretName != "my-svc-tls" {
		t.Errorf("expected SecretName=%q, got %q", "my-svc-tls", vals.Ingress.TLS[0].SecretName)
	}
	if len(vals.Ingress.TLS[0].Hosts) != 1 || vals.Ingress.TLS[0].Hosts[0] != expectedHost {
		t.Errorf("expected TLS host=%q", expectedHost)
	}
}

func TestGenerate_IngressEnabled_NilEnv_NoPanic(t *testing.T) {
	g := helmgen.NewGenerator()
	cfg := minimalConfig()
	cfg.Ingress.Enabled = true

	vals, err := g.Generate(cfg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With nil env, ingress is enabled but no hosts/TLS should be generated.
	if len(vals.Ingress.Hosts) != 0 {
		t.Errorf("expected no hosts with nil env, got %d", len(vals.Ingress.Hosts))
	}
}

func TestGenerate_HealthEndpoint_BothProbesPopulated(t *testing.T) {
	g := helmgen.NewGenerator()
	cfg := minimalConfig()
	cfg.Manage.Health = domain.HealthConfig{
		Endpoint: "/healthz",
		Port:     8081,
	}

	vals, err := g.Generate(cfg, testEnv(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals.LivenessProbe == nil {
		t.Fatal("expected LivenessProbe to be set")
	}
	if vals.ReadinessProbe == nil {
		t.Fatal("expected ReadinessProbe to be set")
	}
	if vals.LivenessProbe.HTTPGet.Path != "/healthz" {
		t.Errorf("expected probe path=%q, got %q", "/healthz", vals.LivenessProbe.HTTPGet.Path)
	}
	if vals.LivenessProbe.HTTPGet.Port != 8081 {
		t.Errorf("expected probe port=8081, got %d", vals.LivenessProbe.HTTPGet.Port)
	}
}

func TestGenerate_NoHealthEndpoint_ProbesNil(t *testing.T) {
	g := helmgen.NewGenerator()
	cfg := minimalConfig()
	// Manage.Health.Endpoint is empty string (zero value)

	vals, err := g.Generate(cfg, testEnv(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals.LivenessProbe != nil {
		t.Error("expected LivenessProbe=nil when no health endpoint")
	}
}

func TestGenerate_MetricsEndpoint_PodAnnotations(t *testing.T) {
	g := helmgen.NewGenerator()
	cfg := minimalConfig()
	cfg.Manage.Metrics = domain.MetricsConfig{
		Endpoint: "/metrics",
		Port:     9090,
	}

	vals, err := g.Generate(cfg, testEnv(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals.PodAnnotations == nil {
		t.Fatal("expected PodAnnotations to be set")
	}
	if vals.PodAnnotations["prometheus.io/scrape"] != "true" {
		t.Errorf("expected prometheus.io/scrape=true, got %q", vals.PodAnnotations["prometheus.io/scrape"])
	}
	if vals.PodAnnotations["prometheus.io/path"] != "/metrics" {
		t.Errorf("expected prometheus.io/path=/metrics, got %q", vals.PodAnnotations["prometheus.io/path"])
	}
	if vals.PodAnnotations["prometheus.io/port"] != "9090" {
		t.Errorf("expected prometheus.io/port=9090, got %q", vals.PodAnnotations["prometheus.io/port"])
	}
}

func TestGenerate_ConfigFile_YAML_Base64Encoded(t *testing.T) {
	g := helmgen.NewGenerator()
	cfg := minimalConfig()
	cfg.Configs = map[string]domain.ConfigFileSpec{
		"app.yml": {
			Format:   "yaml",
			MountDir: "/etc/app",
			Values:   map[string]any{"key": "value"},
		},
	}

	vals, err := g.Generate(cfg, testEnv(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vals.Configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(vals.Configs))
	}
	c := vals.Configs[0]
	if !c.Enabled {
		t.Error("expected config Enabled=true")
	}
	if c.MountLocation != "/etc/app/app.yml" {
		t.Errorf("expected MountLocation=%q, got %q", "/etc/app/app.yml", c.MountLocation)
	}
	// Verify it's valid base64-encoded YAML.
	decoded, err := base64.StdEncoding.DecodeString(c.Data)
	if err != nil {
		t.Fatalf("config data is not valid base64: %v", err)
	}
	if !strings.Contains(string(decoded), "key") {
		t.Errorf("decoded YAML should contain %q, got %q", "key", string(decoded))
	}
}

func TestGenerate_ConfigFile_JSON_Base64Encoded(t *testing.T) {
	g := helmgen.NewGenerator()
	cfg := minimalConfig()
	cfg.Configs = map[string]domain.ConfigFileSpec{
		"config.json": {
			Format:   "json",
			MountDir: "/etc/app",
			Values:   map[string]any{"host": "localhost"},
		},
	}

	vals, err := g.Generate(cfg, testEnv(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(vals.Configs[0].Data)
	if err != nil {
		t.Fatalf("config data is not valid base64: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(decoded, &parsed); err != nil {
		t.Fatalf("decoded data is not valid JSON: %v", err)
	}
	if parsed["host"] != "localhost" {
		t.Errorf("expected host=localhost in JSON, got %v", parsed["host"])
	}
}

func TestGenerate_ConfigFile_Env_Base64Encoded(t *testing.T) {
	g := helmgen.NewGenerator()
	cfg := minimalConfig()
	cfg.Configs = map[string]domain.ConfigFileSpec{
		"app.env": {
			Format:   "env",
			MountDir: "/etc/app",
			Values:   map[string]any{"DB_HOST": "localhost"},
		},
	}

	vals, err := g.Generate(cfg, testEnv(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(vals.Configs[0].Data)
	if err != nil {
		t.Fatalf("config data is not valid base64: %v", err)
	}
	if !strings.Contains(string(decoded), "DB_HOST=localhost") {
		t.Errorf("expected DB_HOST=localhost in env output, got %q", string(decoded))
	}
}

func TestGenerate_ConfigFile_UnsupportedFormat_ReturnsError(t *testing.T) {
	g := helmgen.NewGenerator()
	cfg := minimalConfig()
	cfg.Configs = map[string]domain.ConfigFileSpec{
		"app.toml": {
			Format:   "toml",
			MountDir: "/etc/app",
			Values:   map[string]any{"key": "value"},
		},
	}

	_, err := g.Generate(cfg, testEnv(), nil)
	if err == nil {
		t.Fatal("expected error for unsupported config format, got nil")
	}
}

func TestGenerate_EnvVars_Copied(t *testing.T) {
	g := helmgen.NewGenerator()
	cfg := minimalConfig()
	cfg.Env = map[string]string{"KEY": "val"}

	vals, err := g.Generate(cfg, testEnv(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals.Env["KEY"] != "val" {
		t.Errorf("expected Env[KEY]=%q, got %q", "val", vals.Env["KEY"])
	}
}
