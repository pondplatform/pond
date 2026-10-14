package service

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pondplatform/pond/shared/serviceconfig"
)

func TestRender_PlainString_PassesThrough(t *testing.T) {
	values := map[string]any{"key": "plain-value"}
	got, err := renderTemplate(values, nil, &serviceconfig.ServiceConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["key"] != "plain-value" {
		t.Errorf("expected %q, got %q", "plain-value", got["key"])
	}
}

func TestRender_SingleDepVar_Resolved(t *testing.T) {
	contexts := map[string]map[string]any{
		"db": {"host": "db.internal"},
	}
	values := map[string]any{"dbHost": "{{db.host}}"}

	got, err := renderTemplate(values, contexts, &serviceconfig.ServiceConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["dbHost"] != "db.internal" {
		t.Errorf("expected %q, got %q", "db.internal", got["dbHost"])
	}
}

func TestRender_ServicePortAndReplicas_Resolved(t *testing.T) {
	svc := &serviceconfig.ServiceConfig{
		Deployment: &serviceconfig.DeploymentSpec{Replicas: serviceconfig.Ptr(int32(2))},
		Service:    &serviceconfig.ServiceSpec{Port: serviceconfig.Ptr(int32(8080))},
	}
	values := map[string]any{"port": "{{service.port}}", "replicas": "{{deployment.replicas}}"}

	got, err := renderTemplate(values, nil, svc)
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
	svc := &serviceconfig.ServiceConfig{Env: map[string]string{"DB_URL": "postgres://localhost/mydb"}}
	values := map[string]any{"url": "{{env.DB_URL}}"}

	got, err := renderTemplate(values, nil, svc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["url"] != "postgres://localhost/mydb" {
		t.Errorf("expected %q, got %q", "postgres://localhost/mydb", got["url"])
	}
}

func TestRender_MultipleVarsInOneString(t *testing.T) {
	contexts := map[string]map[string]any{
		"db": {"host": "db.internal", "port": "5432"},
	}
	svc := &serviceconfig.ServiceConfig{Env: map[string]string{"DB_NAME": "mydb"}}
	values := map[string]any{"dsn": "postgres://{{db.host}}:{{db.port}}/{{env.DB_NAME}}"}

	got, err := renderTemplate(values, contexts, svc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "postgres://db.internal:5432/mydb"
	if got["dsn"] != expected {
		t.Errorf("expected %q, got %q", expected, got["dsn"])
	}
}

func TestRender_NestedMap_RecursivelyRendered(t *testing.T) {
	contexts := map[string]map[string]any{
		"cache": {"addr": "redis:6379"},
	}
	values := map[string]any{
		"redis": map[string]any{
			"addr": "{{cache.addr}}",
		},
	}

	got, err := renderTemplate(values, contexts, &serviceconfig.ServiceConfig{})
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

func TestRender_List_RecursivelyRendered(t *testing.T) {
	contexts := map[string]map[string]any{
		"db": {"host": "db.internal", "port": "5432"},
	}
	values := map[string]any{
		"hosts": []any{
			"{{db.host}}",
			map[string]any{"port": "{{db.port}}"},
			[]any{"{{db.host}}:{{db.port}}"},
			7,
		},
	}

	got, err := renderTemplate(values, contexts, &serviceconfig.ServiceConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []any{
		"db.internal",
		map[string]any{"port": "5432"},
		[]any{"db.internal:5432"},
		7,
	}
	if !reflect.DeepEqual(got["hosts"], want) {
		t.Errorf("expected %#v, got %#v", want, got["hosts"])
	}
	if values["hosts"].([]any)[0] != "{{db.host}}" {
		t.Error("input list was mutated")
	}
}

func TestRender_MissingVarInList_ReturnsError(t *testing.T) {
	values := map[string]any{"hosts": []any{"ok", "{{db.host}}"}}

	_, err := renderTemplate(values, nil, &serviceconfig.ServiceConfig{})
	if err == nil {
		t.Fatal("expected error for unresolved variable in list, got nil")
	}
	if !strings.Contains(err.Error(), "db.host") {
		t.Errorf("error missing variable name %q: %s", "db.host", err)
	}
}

func TestRender_MissingVar_ReturnsError(t *testing.T) {
	values := map[string]any{"url": "{{db.host}}"}

	_, err := renderTemplate(values, nil, &serviceconfig.ServiceConfig{})
	if err == nil {
		t.Fatal("expected error for unresolved variable, got nil")
	}
}

func TestRender_MultipleMissingVars_AllNamed(t *testing.T) {
	values := map[string]any{"combined": "{{db.host}}:{{cache.addr}}"}

	_, err := renderTemplate(values, nil, &serviceconfig.ServiceConfig{})
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
	values := map[string]any{
		"count":   42,
		"enabled": true,
		"ratio":   3.14,
		"nothing": nil,
	}

	got, err := renderTemplate(values, nil, &serviceconfig.ServiceConfig{})
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
	got, err := renderTemplate(map[string]any{}, nil, &serviceconfig.ServiceConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestTemplateReferences_FindsEveryVariableOnce(t *testing.T) {
	values := map[string]any{
		"url":    "postgres://{{db.user}}@{{db.host}}",
		"nested": map[string]any{"host": "{{db.host}}", "port": "{{service.port}}"},
		"list":   []any{"{{cache.addr}}", map[string]any{"name": "{{db.database}}"}},
		"count":  3,
	}
	got := templateReferences(values)
	want := []string{"cache.addr", "db.database", "db.host", "db.user", "service.port"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("expected %v, got %v", want, got)
	}
}
