package config_test

import (
	"strings"
	"testing"

	"github.com/pondplatform/pond/internal/common/config"
	"github.com/pondplatform/pond/internal/common/domain"
)

func TestRender_PlainString_PassesThrough(t *testing.T) {
	r := config.NewTemplateRenderer()
	values := map[string]any{"key": "plain-value"}
	got, err := r.Render(values, nil, &domain.ServiceConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["key"] != "plain-value" {
		t.Errorf("expected %q, got %q", "plain-value", got["key"])
	}
}

func TestRender_SingleDepVar_Resolved(t *testing.T) {
	r := config.NewTemplateRenderer()
	contexts := map[string]domain.ResolvedContext{
		"db": {Values: map[string]any{"host": "db.internal"}},
	}
	values := map[string]any{"dbHost": "{{db.host}}"}

	got, err := r.Render(values, contexts, &domain.ServiceConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["dbHost"] != "db.internal" {
		t.Errorf("expected %q, got %q", "db.internal", got["dbHost"])
	}
}

func TestRender_ServicePort_Resolved(t *testing.T) {
	r := config.NewTemplateRenderer()
	svc := &domain.ServiceConfig{Service: domain.ServiceSpec{Port: 8080, Replicas: 2}}
	values := map[string]any{"port": "{{service.port}}", "replicas": "{{service.replicas}}"}

	got, err := r.Render(values, nil, svc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["port"] != "8080" {
		t.Errorf("expected %q, got %q", "8080", got["port"])
	}
	if got["replicas"] != "2" {
		t.Errorf("expected %q, got %q", "2", got["replicas"])
	}
}

func TestRender_EnvVar_Resolved(t *testing.T) {
	r := config.NewTemplateRenderer()
	svc := &domain.ServiceConfig{Env: map[string]string{"DB_URL": "postgres://localhost/mydb"}}
	values := map[string]any{"url": "{{env.DB_URL}}"}

	got, err := r.Render(values, nil, svc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["url"] != "postgres://localhost/mydb" {
		t.Errorf("expected %q, got %q", "postgres://localhost/mydb", got["url"])
	}
}

func TestRender_MultipleVarsInOneString(t *testing.T) {
	r := config.NewTemplateRenderer()
	contexts := map[string]domain.ResolvedContext{
		"db": {Values: map[string]any{"host": "db.internal", "port": "5432"}},
	}
	svc := &domain.ServiceConfig{Env: map[string]string{"DB_NAME": "mydb"}}
	values := map[string]any{"dsn": "postgres://{{db.host}}:{{db.port}}/{{env.DB_NAME}}"}

	got, err := r.Render(values, contexts, svc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "postgres://db.internal:5432/mydb"
	if got["dsn"] != expected {
		t.Errorf("expected %q, got %q", expected, got["dsn"])
	}
}

func TestRender_NestedMap_RecursivelyRendered(t *testing.T) {
	r := config.NewTemplateRenderer()
	contexts := map[string]domain.ResolvedContext{
		"cache": {Values: map[string]any{"addr": "redis:6379"}},
	}
	values := map[string]any{
		"redis": map[string]any{
			"addr": "{{cache.addr}}",
		},
	}

	got, err := r.Render(values, contexts, &domain.ServiceConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nested, ok := got["redis"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map, got %T", got["redis"])
	}
	if nested["addr"] != "redis:6379" {
		t.Errorf("expected %q, got %q", "redis:6379", nested["addr"])
	}
}

func TestRender_MissingVar_ReturnsError(t *testing.T) {
	r := config.NewTemplateRenderer()
	values := map[string]any{"url": "{{db.host}}"}

	_, err := r.Render(values, nil, &domain.ServiceConfig{})
	if err == nil {
		t.Fatal("expected error for unresolved variable, got nil")
	}
}

func TestRender_MultipleMissingVars_AllNamed(t *testing.T) {
	r := config.NewTemplateRenderer()
	// Both {{db.host}} and {{cache.addr}} are missing.
	values := map[string]any{"combined": "{{db.host}}:{{cache.addr}}"}

	_, err := r.Render(values, nil, &domain.ServiceConfig{})
	if err == nil {
		t.Fatal("expected error for unresolved variables, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "db.host") {
		t.Errorf("error missing variable name %q: %s", "db.host", msg)
	}
	if !strings.Contains(msg, "cache.addr") {
		t.Errorf("error missing variable name %q: %s", "cache.addr", msg)
	}
}

func TestRender_NonStringValue_PassesThrough(t *testing.T) {
	r := config.NewTemplateRenderer()
	values := map[string]any{
		"count":   42,
		"enabled": true,
		"ratio":   3.14,
		"nothing": nil,
	}

	got, err := r.Render(values, nil, &domain.ServiceConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["count"] != 42 {
		t.Errorf("expected 42, got %v", got["count"])
	}
	if got["enabled"] != true {
		t.Errorf("expected true, got %v", got["enabled"])
	}
	if got["ratio"] != 3.14 {
		t.Errorf("expected 3.14, got %v", got["ratio"])
	}
	if got["nothing"] != nil {
		t.Errorf("expected nil, got %v", got["nothing"])
	}
}

func TestRender_EmptyValues_ReturnsEmpty(t *testing.T) {
	r := config.NewTemplateRenderer()
	got, err := r.Render(map[string]any{}, nil, &domain.ServiceConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

