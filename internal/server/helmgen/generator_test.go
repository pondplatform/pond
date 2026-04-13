package helmgen

import (
	"encoding/base64"
	"testing"

	"github.com/pondplatform/pond/internal/common/domain"
)

func TestGenerator_Generate_ConfigFiles(t *testing.T) {
	g := NewGenerator()

	// Values are pre-rendered by the deployment service before calling Generate
	cfg := &domain.ServiceConfig{
		Name: "test-service",
		Configs: map[string]domain.ConfigFileSpec{
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

	vals, err := g.Generate(cfg, env, nil)
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

func TestGenerator_Generate_Ingress(t *testing.T) {
	g := NewGenerator()

	cfg := &domain.ServiceConfig{
		Name: "web",
		Ingress: domain.IngressConfig{
			Enabled: true,
		},
	}

	env := &domain.Environment{
		DefaultIngressBaseHost: "pond.local",
	}

	vals, err := g.Generate(cfg, env, nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if !vals.Ingress.Enabled {
		t.Fatal("Expected ingress to be enabled")
	}

	if vals.Ingress.Annotations["cert-manager.io/cluster-issuer"] != "letsencrypt-prod" {
		t.Errorf("Missing or wrong cluster-issuer annotation: %v", vals.Ingress.Annotations)
	}

	if len(vals.Ingress.Hosts) != 1 || vals.Ingress.Hosts[0].Host != "web.pond.local" {
		t.Errorf("Unexpected ingress host: %v", vals.Ingress.Hosts)
	}
}
