package service

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

func gateway(hostname *string) *serviceconfig.GatewayConfig {
	return &serviceconfig.GatewayConfig{HTTP: &serviceconfig.GatewayHTTPConfig{Enabled: serviceconfig.Ptr(true), Hostname: hostname}}
}

func TestNewTemplateContext(t *testing.T) {
	cfg := &serviceconfig.ServiceConfig{
		Name:       "web",
		Service:    &serviceconfig.ServiceSpec{Port: serviceconfig.Ptr(int32(8080))},
		Deployment: &serviceconfig.DeploymentSpec{Replicas: serviceconfig.Ptr(int32(2))},
		Gateway:    gateway(nil),
	}
	env := &db.Environment{Name: "staging", DefaultIngressBaseHost: "e2e.local"}

	got := newTemplateContext(cfg, env, map[string]string{"SIGNING_KEY": "s3cr3t"})
	want := templateContext{
		"service.name":          "web",
		"service.port":          "8080",
		"deployment.replicas":   "2",
		"environment.name":      "staging",
		"gateway.hostname":      "web.e2e.local",
		"gateway.url":           "https://web.e2e.local",
		"variables.SIGNING_KEY": "s3cr3t",
	}
	if !maps.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestNewTemplateContext_OmitsWhatIsNotSet(t *testing.T) {
	got := newTemplateContext(&serviceconfig.ServiceConfig{Name: "web"}, &db.Environment{Name: "staging"}, nil)
	want := templateContext{"service.name": "web", "environment.name": "staging"}
	if !maps.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestNewTemplateContext_Gateway(t *testing.T) {
	cases := map[string]struct {
		gateway  *serviceconfig.GatewayConfig
		baseHost string
		want     string
	}{
		"explicit hostname wins":       {gateway(serviceconfig.Ptr("api.example.com")), "e2e.local", "api.example.com"},
		"derived from the base host":   {gateway(nil), "e2e.local", "web.e2e.local"},
		"no hostname resolves":         {gateway(nil), "", ""},
		"a wildcard is no one host":    {gateway(serviceconfig.Ptr("*.example.com")), "e2e.local", ""},
		"gateway unset":                {nil, "e2e.local", ""},
		"gateway explicitly disabled":  {&serviceconfig.GatewayConfig{HTTP: &serviceconfig.GatewayHTTPConfig{Enabled: serviceconfig.Ptr(false)}}, "e2e.local", ""},
		"hostname without the gateway": {&serviceconfig.GatewayConfig{HTTP: &serviceconfig.GatewayHTTPConfig{Hostname: serviceconfig.Ptr("api.example.com")}}, "e2e.local", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := &serviceconfig.ServiceConfig{Name: "web", Gateway: tc.gateway}
			got := newTemplateContext(cfg, &db.Environment{DefaultIngressBaseHost: tc.baseHost}, nil)
			hostname, hasHostname := got["gateway.hostname"]
			url, hasURL := got["gateway.url"]
			if tc.want == "" {
				if hasHostname || hasURL {
					t.Errorf("expected no gateway values, got hostname %q and url %q", hostname, url)
				}
				return
			}
			if hostname != tc.want || url != "https://"+tc.want {
				t.Errorf("expected hostname %q and url https://%s, got %q and %q", tc.want, tc.want, hostname, url)
			}
		})
	}
}

func TestWithDependencies(t *testing.T) {
	base := templateContext{"service.name": "web"}
	got := base.withDependencies(map[string]map[string]any{"db": {"host": "db.internal", "port": float64(5432)}})
	want := templateContext{
		"service.name":         "web",
		"dependencies.db.host": "db.internal",
		"dependencies.db.port": "5432",
	}
	if !maps.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
	if len(base) != 1 {
		t.Errorf("the receiver was modified: %v", base)
	}
}

func TestParseDependencyRef(t *testing.T) {
	cases := []struct {
		ref, name, key string
		ok             bool
	}{
		{"dependencies.db.host", "db", "host", true},
		{"dependencies.db", "db", "", false},
		{"db.host", "", "", false},
		{"variables.db", "", "", false},
	}
	for _, tc := range cases {
		name, key, ok := parseDependencyRef(tc.ref)
		if name != tc.name || key != tc.key || ok != tc.ok {
			t.Errorf("%s: expected (%q, %q, %v), got (%q, %q, %v)", tc.ref, tc.name, tc.key, tc.ok, name, key, ok)
		}
	}
}

func TestRenderServiceConfig(t *testing.T) {
	cfg := serviceconfig.ServiceConfig{
		Name:    "web",
		Command: []string{"serve", "--port={{service.port}}"},
		Env: map[string]string{
			"DATABASE_HOST": "{{dependencies.db.host}}",
			"PUBLIC_URL":    "{{gateway.url}}/app",
			"LOG_LEVEL":     "info",
		},
		Configs: map[string]serviceconfig.ConfigFileSpec{
			"app.yaml": {Format: "yaml", MountPath: "/etc/app.yaml", Values: map[string]any{"name": "{{variables.PUBLIC_NAME}}"}},
		},
	}
	ctx := templateContext{
		"service.port":          "8080",
		"gateway.url":           "https://web.e2e.local",
		"variables.PUBLIC_NAME": "Acme",
		"dependencies.db.host":  "db.internal",
	}

	got, err := renderServiceConfig(cfg, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantEnv := map[string]string{"DATABASE_HOST": "db.internal", "PUBLIC_URL": "https://web.e2e.local/app", "LOG_LEVEL": "info"}
	if !maps.Equal(got.Env, wantEnv) {
		t.Errorf("env: expected %v, got %v", wantEnv, got.Env)
	}
	if want := []string{"serve", "--port=8080"}; !reflect.DeepEqual(got.Command, want) {
		t.Errorf("command: expected %q, got %q", want, got.Command)
	}
	if name := got.Configs["app.yaml"].Values["name"]; name != "Acme" {
		t.Errorf("config: expected Acme, got %v", name)
	}
	if cfg.Env["DATABASE_HOST"] != "{{dependencies.db.host}}" || cfg.Command[1] != "--port={{service.port}}" ||
		cfg.Configs["app.yaml"].Values["name"] != "{{variables.PUBLIC_NAME}}" {
		t.Errorf("the input config was modified: %+v", cfg)
	}
}

func TestRenderServiceConfig_IsSinglePass(t *testing.T) {
	cfg := serviceconfig.ServiceConfig{Env: map[string]string{"KEY": "{{variables.KEY}}"}}
	ctx := templateContext{"variables.KEY": "{{service.name}}", "service.name": "web"}

	got, err := renderServiceConfig(cfg, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Env["KEY"] != "{{service.name}}" {
		t.Errorf("expected the variable's value verbatim, got %q", got.Env["KEY"])
	}
}

func TestRenderServiceConfig_ErrorsNameTheField(t *testing.T) {
	cases := map[string]struct {
		cfg  serviceconfig.ServiceConfig
		want string
	}{
		"env":     {serviceconfig.ServiceConfig{Env: map[string]string{"DB_USER": "{{dependencies.db.username}}"}}, `env["DB_USER"]`},
		"command": {serviceconfig.ServiceConfig{Command: []string{"serve", "{{dependencies.db.username}}"}}, "command[1]"},
		"config": {serviceconfig.ServiceConfig{Configs: map[string]serviceconfig.ConfigFileSpec{
			"app.yaml": {Values: map[string]any{"user": "{{dependencies.db.username}}"}},
		}}, `config "app.yaml"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := renderServiceConfig(tc.cfg, templateContext{})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "dependencies.db.username") {
				t.Errorf("expected an error naming %s and the variable, got %v", tc.want, err)
			}
		})
	}
}

func TestRender_PlainString_PassesThrough(t *testing.T) {
	got, err := renderMap(map[string]any{"key": "plain-value"}, templateContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["key"] != "plain-value" {
		t.Errorf("expected %q, got %q", "plain-value", got["key"])
	}
}

func TestRender_MultipleVarsInOneString(t *testing.T) {
	ctx := templateContext{"dependencies.db.host": "db.internal", "dependencies.db.port": "5432", "variables.DB_NAME": "mydb"}
	values := map[string]any{"dsn": "postgres://{{dependencies.db.host}}:{{dependencies.db.port}}/{{variables.DB_NAME}}"}

	got, err := renderMap(values, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "postgres://db.internal:5432/mydb"
	if got["dsn"] != expected {
		t.Errorf("expected %q, got %q", expected, got["dsn"])
	}
}

func TestRender_NestedMap_RecursivelyRendered(t *testing.T) {
	values := map[string]any{
		"redis": map[string]any{
			"addr": "{{dependencies.cache.addr}}",
		},
	}

	got, err := renderMap(values, templateContext{"dependencies.cache.addr": "redis:6379"})
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
	ctx := templateContext{"dependencies.db.host": "db.internal", "dependencies.db.port": "5432"}
	values := map[string]any{
		"hosts": []any{
			"{{dependencies.db.host}}",
			map[string]any{"port": "{{dependencies.db.port}}"},
			[]any{"{{dependencies.db.host}}:{{dependencies.db.port}}"},
			7,
		},
	}

	got, err := renderMap(values, ctx)
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
	if values["hosts"].([]any)[0] != "{{dependencies.db.host}}" {
		t.Error("input list was mutated")
	}
}

func TestRender_MissingVarInList_ReturnsError(t *testing.T) {
	values := map[string]any{"hosts": []any{"ok", "{{dependencies.db.host}}"}}

	_, err := renderMap(values, templateContext{})
	if err == nil {
		t.Fatal("expected error for unresolved variable in list, got nil")
	}
	if !strings.Contains(err.Error(), "dependencies.db.host") {
		t.Errorf("error missing variable name %q: %s", "dependencies.db.host", err)
	}
}

func TestRender_MultipleMissingVars_AllNamed(t *testing.T) {
	values := map[string]any{"combined": "{{dependencies.db.host}}:{{variables.PORT}}"}

	_, err := renderMap(values, templateContext{})
	if err == nil {
		t.Fatal("expected error for unresolved variables, got nil")
	}
	msg := err.Error()
	for _, name := range []string{"dependencies.db.host", "variables.PORT"} {
		if !strings.Contains(msg, name) {
			t.Errorf("error missing variable name %q: %s", name, msg)
		}
	}
}

func TestRender_EnvIsNoLongerASource(t *testing.T) {
	cfg := serviceconfig.ServiceConfig{
		Env:     map[string]string{"DB_NAME": "mydb"},
		Configs: map[string]serviceconfig.ConfigFileSpec{"app.yaml": {Values: map[string]any{"db": "{{env.DB_NAME}}"}}},
	}
	if _, err := renderServiceConfig(cfg, newTemplateContext(&cfg, &db.Environment{}, nil)); err == nil || !strings.Contains(err.Error(), "env.DB_NAME") {
		t.Errorf("expected {{env.DB_NAME}} to be unresolved, got %v", err)
	}
}

func TestRender_NonStringValue_PassesThrough(t *testing.T) {
	values := map[string]any{
		"count":   42,
		"enabled": true,
		"ratio":   3.14,
		"nothing": nil,
	}

	got, err := renderMap(values, templateContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, values) {
		t.Errorf("expected %v, got %v", values, got)
	}
}

func TestRender_EmptyValues_ReturnsEmpty(t *testing.T) {
	got, err := renderMap(map[string]any{}, templateContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestTemplateReferences_FindsEveryVariableOnce(t *testing.T) {
	values := map[string]any{
		"url":    "postgres://{{dependencies.db.user}}@{{dependencies.db.host}}",
		"nested": map[string]any{"host": "{{dependencies.db.host}}", "port": "{{service.port}}"},
		"list":   []any{"{{variables.CACHE}}", map[string]any{"name": "{{dependencies.db.database}}"}},
		"count":  3,
	}
	got := templateReferences(values)
	want := []string{"dependencies.db.database", "dependencies.db.host", "dependencies.db.user", "service.port", "variables.CACHE"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("expected %v, got %v", want, got)
	}
}
