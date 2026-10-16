package service

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/dependency"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
)

var testProjectID = uuid.New()

// inlineTx runs every transaction directly against repos.
type inlineTx struct{ repos store.Repos }

func (t inlineTx) RunInTx(ctx context.Context, fn func(ctx context.Context, tx store.Repos) error) error {
	return fn(ctx, t.repos)
}

func newTestService(deployments *testutil.MockDeploymentInfoStore) *deploymentService {
	repos := store.Repos{
		Deployments: deployments,
		Services:    &testutil.MockServiceRepository{},
		Envs: &testutil.MockEnvironmentRepository{
			GetByNameFn: func(_ context.Context, _ uuid.UUID, name string) (*db.Environment, error) {
				if name != "staging" {
					return nil, api.NotFound("environment %q not found", name)
				}
				return &db.Environment{ID: uuid.New(), Name: name}, nil
			},
			ListByProjectFn: func(context.Context, uuid.UUID) ([]db.Environment, error) {
				return []db.Environment{{Name: "staging"}, {Name: "prod"}}, nil
			},
		},
	}
	return &deploymentService{
		repos:    repos,
		tx:       inlineTx{repos},
		specs:    dependency.NewSpecRegistry(),
		resolver: config.NewResolver(),
		machine:  testMachine(),
		log:      slog.New(slog.DiscardHandler),
		now:      time.Now,
	}
}

func submitRequest(deps map[string]serviceconfig.DependencyDeclaration, inputs map[string]api.DependencyInput) api.SubmitRequest {
	return api.SubmitRequest{
		ProjectID:       testProjectID,
		EnvironmentName: "staging",
		OverridableConfig: serviceconfig.OverridableConfig{
			ServiceConfig: serviceconfig.ServiceConfig{
				Version:      1,
				Name:         "my-service",
				Image:        "ghcr.io/example/my-service",
				Dependencies: deps,
			},
		},
		ImageTag:        "v1",
		TriggeredBy:     "user",
		DeploymentInput: api.DeploymentInput{Dependencies: inputs},
	}
}

func withConfigValues(req api.SubmitRequest, values map[string]any) api.SubmitRequest {
	req.OverridableConfig.ServiceConfig.Configs = map[string]serviceconfig.ConfigFileSpec{
		"app.env": {Format: ".env", MountPath: "/etc/app/app.env", Values: values},
	}
	return req
}

func withCommand(req api.SubmitRequest, args ...string) api.SubmitRequest {
	req.OverridableConfig.ServiceConfig.Command = args
	return req
}

func withEnv(req api.SubmitRequest, env map[string]string) api.SubmitRequest {
	req.OverridableConfig.ServiceConfig.Env = env
	return req
}

func withVariables(req api.SubmitRequest, declared map[string]serviceconfig.VariableDeclaration, values map[string]string) api.SubmitRequest {
	req.OverridableConfig.ServiceConfig.Variables = declared
	req.Variables = values
	return req
}

func withGateway(req api.SubmitRequest, hostname *string) api.SubmitRequest {
	return withGatewayRule(req, hostname)
}

func withGatewayRule(req api.SubmitRequest, hostname *string, rules ...serviceconfig.HTTPRouteRule) api.SubmitRequest {
	req.OverridableConfig.ServiceConfig.Gateway = gatewayWithRules(hostname, rules...)
	return req
}

const unknownReference = "names no known value (service.name, service.port, deployment.replicas, environment.name, gateway.hostname, gateway.port, gateway.url, variables.<name>, dependencies.<name>.<output>)"

const visibleArguments = ", and arguments are visible to anyone who can read the workload; pass it in env or a config file instead"

func validationErrors(t *testing.T, err error) []api.ValidationError {
	t.Helper()
	var ve *api.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected *api.ValidationErrors so the handler returns 422, got %T: %v", err, err)
	}
	return ve.Errors
}

var postgresDep = map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}}

func TestValidateSubmission_AcceptsAValidRequest(t *testing.T) {
	s := newTestService(&testutil.MockDeploymentInfoStore{})
	req := withConfigValues(submitRequest(postgresDep, nil), map[string]any{
		"DB_HOST": "{{dependencies.db.host}}",
	})
	env, cfg, err := s.validateSubmission(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Name != "staging" || cfg.Name != "my-service" {
		t.Errorf("expected staging and my-service, got %q and %q", env.Name, cfg.Name)
	}
}

func TestValidateSubmission_Rejects(t *testing.T) {
	cases := map[string]struct {
		req  api.SubmitRequest
		want []api.ValidationError
	}{
		"undeclared input": {
			req:  submitRequest(postgresDep, map[string]api.DependencyInput{"db": {Managed: true}, "cache": {Managed: true}}),
			want: []api.ValidationError{{Component: "dependency", Field: "cache", Message: "not declared in service config"}},
		},
		"unknown dependency type": {
			req:  submitRequest(map[string]serviceconfig.DependencyDeclaration{"q": {Type: "kafka"}}, nil),
			want: []api.ValidationError{{Component: "dependency", Field: "q", Message: `unknown dependency type "kafka"`}},
		},
		"template variable naming nothing": {
			req: withConfigValues(submitRequest(postgresDep, nil), map[string]any{"URL": "{{cache.url}}"}),
			want: []api.ValidationError{{Component: "config", Field: "app.env",
				Message: "template variable {{cache.url}} " + unknownReference}},
		},
		"undeclared dependency": {
			req: withConfigValues(submitRequest(postgresDep, nil), map[string]any{"URL": "{{dependencies.cache.url}}"}),
			want: []api.ValidationError{{Component: "config", Field: "app.env",
				Message: `template variable {{dependencies.cache.url}} names no declared dependency: "cache" is not under dependencies in pond.yml`}},
		},
		"undeclared dependency inside a list": {
			req: withConfigValues(submitRequest(postgresDep, nil), map[string]any{"URLS": []any{"{{dependencies.db.host}}", "{{dependencies.cache.url}}"}}),
			want: []api.ValidationError{{Component: "config", Field: "app.env",
				Message: `template variable {{dependencies.cache.url}} names no declared dependency: "cache" is not under dependencies in pond.yml`}},
		},
		"undeclared dependency in the command": {
			req: withCommand(submitRequest(postgresDep, nil), "serve", "--cache={{dependencies.cache.url}}"),
			want: []api.ValidationError{{Component: "config", Field: "command[1]",
				Message: `template variable {{dependencies.cache.url}} names no declared dependency: "cache" is not under dependencies in pond.yml`}},
		},
		"undeclared dependency in env": {
			req: withEnv(submitRequest(postgresDep, nil), map[string]string{"CACHE_URL": "{{dependencies.cache.url}}", "DB_HOST": "{{dependencies.db.host}}"}),
			want: []api.ValidationError{{Component: "config", Field: `env["CACHE_URL"]`,
				Message: `template variable {{dependencies.cache.url}} names no declared dependency: "cache" is not under dependencies in pond.yml`}},
		},
		"a dependency without an output": {
			req: withConfigValues(submitRequest(postgresDep, nil), map[string]any{"DB": "{{dependencies.db}}"}),
			want: []api.ValidationError{{Component: "config", Field: "app.env",
				Message: "template variable {{dependencies.db}} names no output: write {{dependencies.db.<output>}}"}},
		},
		"a dependency output without the dependencies prefix": {
			req: withEnv(submitRequest(postgresDep, nil), map[string]string{"DB_HOST": "{{db.host}}"}),
			want: []api.ValidationError{{Component: "config", Field: `env["DB_HOST"]`,
				Message: "template variable {{db.host}} names no known value: did you mean {{dependencies.db.host}}?"}},
		},
		"env as a source": {
			req: withConfigValues(withEnv(submitRequest(nil, nil), map[string]string{"LOG_LEVEL": "debug"}), map[string]any{"LEVEL": "{{env.LOG_LEVEL}}"}),
			want: []api.ValidationError{{Component: "config", Field: "app.env",
				Message: "template variable {{env.LOG_LEVEL}} is no longer available: declare a variable and use {{variables.<name>}} wherever the value is needed, env included"}},
		},
		"undeclared variable": {
			req: withEnv(submitRequest(nil, nil), map[string]string{"KEY": "{{variables.KEY}}"}),
			want: []api.ValidationError{{Component: "config", Field: `env["KEY"]`,
				Message: `template variable {{variables.KEY}} names no declared variable: "KEY" is not under variables in pond.yml`}},
		},
		"gateway values without the gateway": {
			req: withEnv(submitRequest(nil, nil), map[string]string{"PUBLIC_URL": "{{gateway.url}}"}),
			want: []api.ValidationError{{Component: "config", Field: `env["PUBLIC_URL"]`,
				Message: "template variable {{gateway.url}} needs the gateway: set gateway.http.enabled"}},
		},
		// The test environment has no base host to derive one from.
		"gateway values without a hostname": {
			req: withEnv(withGateway(submitRequest(nil, nil), nil), map[string]string{"HOST": "{{gateway.hostname}}"}),
			want: []api.ValidationError{{Component: "config", Field: `env["HOST"]`,
				Message: `template variable {{gateway.hostname}} needs a hostname: set gateway.http.hostname, or a base host on environment "staging"`}},
		},
		"gateway values with a wildcard hostname": {
			req: withEnv(withGateway(submitRequest(nil, nil), serviceconfig.Ptr("*.example.com")), map[string]string{"PUBLIC_URL": "{{gateway.url}}"}),
			want: []api.ValidationError{{Component: "config", Field: `env["PUBLIC_URL"]`,
				Message: `template variable {{gateway.url}} needs a single hostname, and gateway.http.hostname "*.example.com" is a wildcard`}},
		},
		"sensitive output in the command": {
			req: withCommand(submitRequest(postgresDep, nil), "serve", "--db", "postgres://{{dependencies.db.username}}:{{dependencies.db.password}}@{{dependencies.db.host}}"),
			want: []api.ValidationError{{Component: "config", Field: "command[2]",
				Message: "template variable {{dependencies.db.password}} is sensitive" + visibleArguments}},
		},
		"variable of another service in the command": {
			req: withCommand(submitRequest(map[string]serviceconfig.DependencyDeclaration{
				"api": {Type: "service", Config: map[string]any{"service": "backend"}},
			}, nil), "--key={{dependencies.api.variables.API_KEY}}"),
			want: []api.ValidationError{{Component: "config", Field: "command[0]",
				Message: "template variable {{dependencies.api.variables.API_KEY}} is a variable of another service, which may be secret" + visibleArguments}},
		},
		// An unmanaged input may carry any key, and nothing says whether it is a secret.
		"undeclared output in the command": {
			req: withCommand(submitRequest(map[string]serviceconfig.DependencyDeclaration{"api": {Type: "http-service"}}, nil), "--token={{dependencies.api.token}}"),
			want: []api.ValidationError{{Component: "config", Field: "command[0]",
				Message: `template variable {{dependencies.api.token}} may be sensitive: http-service declares no "token" output` + visibleArguments}},
		},
		"secret variable in the command": {
			req: withCommand(withVariables(submitRequest(nil, nil),
				map[string]serviceconfig.VariableDeclaration{"KEY": {Secret: true}, "NAME": {}},
				map[string]string{"KEY": "s3cr3t", "NAME": "acme"}), "serve", "--name={{variables.NAME}}", "--key={{variables.KEY}}"),
			want: []api.ValidationError{{Component: "config", Field: "command[2]",
				Message: "template variable {{variables.KEY}} is a secret variable" + visibleArguments}},
		},
		"variable input not declared": {
			req:  withVariables(submitRequest(nil, nil), nil, map[string]string{"EXTRA": "x"}),
			want: []api.ValidationError{{Component: "variable", Field: "EXTRA", Message: "not declared in pond.yml"}},
		},
		"variable without a value": {
			req: withEnv(withVariables(submitRequest(nil, nil),
				map[string]serviceconfig.VariableDeclaration{"KEY": {Secret: true}, "NAME": {}}, map[string]string{"NAME": ""}),
				map[string]string{"KEY": "{{variables.KEY}}"}),
			want: []api.ValidationError{{Component: "variable", Field: "KEY", Message: "missing value: the input gives none"}},
		},
		"gateway checked as rendered": {
			req: withVariables(withGateway(submitRequest(nil, nil), serviceconfig.Ptr("{{variables.CUSTOMER}}.example.com")),
				map[string]serviceconfig.VariableDeclaration{"CUSTOMER": {}}, map[string]string{"CUSTOMER": "Acme Corp"}),
			want: []api.ValidationError{{Component: "config", Field: "gateway.http.hostname",
				Message: `must be a lowercase DNS hostname without scheme, port or path, got "Acme Corp.example.com"`}},
		},
		"gateway hostname rendered to a wildcard": {
			req: withEnv(withVariables(withGateway(submitRequest(nil, nil), serviceconfig.Ptr("{{variables.HOST}}")),
				map[string]serviceconfig.VariableDeclaration{"HOST": {}}, map[string]string{"HOST": "*.example.com"}),
				map[string]string{"PUBLIC_URL": "{{gateway.url}}"}),
			want: []api.ValidationError{{Component: "config", Field: `env["PUBLIC_URL"]`,
				Message: `template variable {{gateway.url}} needs a single hostname, and gateway.http.hostname "{{variables.HOST}}" is a wildcard`}},
		},
		// Left unrendered, the hostname isn't checked as one: only the reference is wrong.
		"undeclared dependency in the gateway": {
			req: withGateway(submitRequest(postgresDep, nil), serviceconfig.Ptr("{{dependencies.cache.host}}")),
			want: []api.ValidationError{{Component: "config", Field: "gateway.http.hostname",
				Message: `template variable {{dependencies.cache.host}} names no declared dependency: "cache" is not under dependencies in pond.yml`}},
		},
		"gateway values in the gateway": {
			req: withGatewayRule(submitRequest(nil, nil), serviceconfig.Ptr("web.example.com"), templatedRule("{{gateway.url}}", "{{gateway.hostname}}")),
			want: []api.ValidationError{
				{Component: "config", Field: "gateway.http.rules[0].filters[0].requestHeaderModifier.set[0].value",
					Message: "template variable {{gateway.url}} comes from the gateway, so the gateway can't use it"},
				{Component: "config", Field: "gateway.http.rules[0].filters[1].requestRedirect.hostname",
					Message: "template variable {{gateway.hostname}} comes from the gateway, so the gateway can't use it"},
			},
		},
		"secret variable in the gateway": {
			req: withVariables(withGatewayRule(submitRequest(nil, nil), nil, templatedRule("Bearer {{variables.TOKEN}}", "web.example.com")),
				map[string]serviceconfig.VariableDeclaration{"TOKEN": {Secret: true}}, map[string]string{"TOKEN": "s3cr3t"}),
			want: []api.ValidationError{{Component: "config", Field: "gateway.http.rules[0].filters[0].requestHeaderModifier.set[0].value",
				Message: "template variable {{variables.TOKEN}} is a secret variable, and the gateway's settings are visible to anyone who can read the HTTPRoute"}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newTestService(&testutil.MockDeploymentInfoStore{})
			_, _, err := s.validateSubmission(context.Background(), tc.req)
			got := validationErrors(t, err)
			if len(got) != len(tc.want) {
				t.Fatalf("expected %+v, got %+v", tc.want, got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("error %d: expected %+v, got %+v", i, tc.want[i], got[i])
				}
			}
		})
	}
}

func TestValidateSubmission_AcceptsEveryKnownValue(t *testing.T) {
	s := newTestService(&testutil.MockDeploymentInfoStore{})
	req := withVariables(submitRequest(postgresDep, nil),
		map[string]serviceconfig.VariableDeclaration{"SIGNING_KEY": {Secret: true}, "LOG_LEVEL": {}},
		map[string]string{"SIGNING_KEY": "s3cr3t", "LOG_LEVEL": "debug"})
	req = withGateway(req, serviceconfig.Ptr("web.example.com"))
	req = withEnv(req, map[string]string{
		"SIGNING_KEY": "{{variables.SIGNING_KEY}}",
		"DB_PASSWORD": "{{dependencies.db.password}}",
		"PUBLIC_URL":  "{{gateway.url}}",
	})
	req = withConfigValues(req, map[string]any{
		"PORT":     "{{service.port}}",
		"REPLICAS": "{{deployment.replicas}}",
		"NAME":     "{{service.name}} in {{environment.name}} at {{gateway.hostname}}",
		"LEVEL":    "{{variables.LOG_LEVEL}}",
	})
	req.OverridableConfig.ServiceConfig.Service = &serviceconfig.ServiceSpec{Port: serviceconfig.Ptr(int32(8080))}
	req.OverridableConfig.ServiceConfig.Deployment = &serviceconfig.DeploymentSpec{Replicas: serviceconfig.Ptr(int32(2))}
	if _, _, err := s.validateSubmission(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSubmission_AcceptsATemplatedGateway(t *testing.T) {
	cases := map[string]api.SubmitRequest{
		"every field rendered now": withVariables(withGatewayRule(submitRequest(nil, nil),
			serviceconfig.Ptr("{{service.name}}-{{environment.name}}.example.com"), templatedRule("{{variables.UPSTREAM}}", "old.{{variables.DOMAIN}}")),
			map[string]serviceconfig.VariableDeclaration{"UPSTREAM": {}, "DOMAIN": {}}, map[string]string{"UPSTREAM": "http://api", "DOMAIN": "example.com"}),
		// A dependency's outputs are only known, and checked, at rollout.
		"a hostname from a dependency": withGatewayRule(submitRequest(postgresDep, nil),
			serviceconfig.Ptr("{{dependencies.db.host}}"), templatedRule("{{dependencies.db.host}}", "{{dependencies.db.host}}")),
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			req = withEnv(req, map[string]string{"PUBLIC_URL": "{{gateway.url}}"})
			s := newTestService(&testutil.MockDeploymentInfoStore{})
			_, cfg, err := s.validateSubmission(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(*cfg.Gateway.HTTP.Hostname, "{{") {
				t.Errorf("expected the config kept as written for the rollout to render, got hostname %q", *cfg.Gateway.HTTP.Hostname)
			}
		})
	}
}

func TestValidateSubmission_AcceptsNonSensitiveReferencesInTheCommand(t *testing.T) {
	s := newTestService(&testutil.MockDeploymentInfoStore{})
	deps := map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}, "api": {Type: "http-service"}}
	req := withCommand(submitRequest(deps, nil),
		"serve", "--port", "{{service.port}}", "--db={{dependencies.db.host}}:{{dependencies.db.port}}", "--api", "{{dependencies.api.url}}", "--log-level", "{{variables.LOG_LEVEL}}")
	req = withVariables(req, map[string]serviceconfig.VariableDeclaration{"LOG_LEVEL": {}}, map[string]string{"LOG_LEVEL": "debug"})
	req.OverridableConfig.ServiceConfig.Service = &serviceconfig.ServiceSpec{Port: serviceconfig.Ptr(int32(8080))}
	if _, _, err := s.validateSubmission(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// The rollout runs from the deployment the submit creates, so its variables
// have to be on it both in the store and in memory.
func TestSubmit_KeepsTheVariablesForTheRollout(t *testing.T) {
	var stored map[string]string
	var helm *db.Command
	deployments := &testutil.MockDeploymentInfoStore{
		CreateFn: func(_ context.Context, d *db.Deployment) error {
			stored = d.Variables
			return nil
		},
		CreateCommandFn: func(_ context.Context, cmd *db.Command) error {
			helm = cmd
			return nil
		},
	}
	s := newTestService(deployments)
	s.repos.Services.(*testutil.MockServiceRepository).GetByNameFn = func(context.Context, uuid.UUID, string) (*db.Service, error) {
		return &db.Service{ID: uuid.New(), ProjectID: testProjectID, Name: "my-service"}, nil
	}
	s.repos.Envs.(*testutil.MockEnvironmentRepository).GetByIDFn = func(context.Context, uuid.UUID) (*db.Environment, error) {
		return &testEnv, nil
	}
	values := map[string]string{"SIGNING_KEY": "s3cr3t"}
	req := withEnv(withVariables(submitRequest(nil, nil),
		map[string]serviceconfig.VariableDeclaration{"SIGNING_KEY": {Secret: true}}, values),
		map[string]string{"SIGNING_KEY": "{{variables.SIGNING_KEY}}"})

	if _, err := s.Submit(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !maps.Equal(stored, values) {
		t.Errorf("expected the variables stored with the deployment, got %v", stored)
	}
	if helm == nil || helm.Type != db.CommandTypeHelmUpgrade {
		t.Fatalf("expected a helm command, got %+v", helm)
	}
	if env := helmValues(t, helm).Env; env["SIGNING_KEY"] != "s3cr3t" {
		t.Errorf("expected the variable rendered into env, got %v", env)
	}
}

func TestValidateSubmission_RejectsAnInvalidProbe(t *testing.T) {
	s := newTestService(&testutil.MockDeploymentInfoStore{})
	req := submitRequest(nil, nil)
	req.OverridableConfig.ServiceConfig.Deployment = &serviceconfig.DeploymentSpec{
		Management: &serviceconfig.ManagementConfig{
			// No leading slash, and no service port to resolve against.
			ReadinessProbe: &serviceconfig.Probe{
				HTTPGet: &serviceconfig.HTTPGetAction{Path: "health"},
			},
		},
	}
	_, _, err := s.validateSubmission(context.Background(), req)
	if got := validationErrors(t, err); len(got) != 2 {
		t.Errorf("expected errors for both path and port, got %+v", got)
	}
}

func TestValidateSubmission_UnknownEnvironmentListsTheAvailableOnes(t *testing.T) {
	s := newTestService(&testutil.MockDeploymentInfoStore{})
	req := submitRequest(nil, nil)
	req.EnvironmentName = "stagin"
	_, _, err := s.validateSubmission(context.Background(), req)
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if !strings.Contains(err.Error(), "available: prod, staging") {
		t.Errorf("expected the available environments in %q", err)
	}
}

func TestSubmit_WritesNothingWhenValidationFails(t *testing.T) {
	deployments := &testutil.MockDeploymentInfoStore{
		CreateFn: func(context.Context, *db.Deployment) error {
			t.Error("no deployment should be created when validation fails")
			return nil
		},
	}
	s := newTestService(deployments)
	undeclared := map[string]api.DependencyInput{"db": {Managed: true}, "cache": {Managed: true}}
	if _, err := s.Submit(context.Background(), submitRequest(postgresDep, undeclared)); err == nil {
		t.Fatal("expected a validation error")
	}
}

func TestVerifyUnmanagedReferences(t *testing.T) {
	cfg := withConfigValues(submitRequest(nil, nil), map[string]any{
		"API_URL":   "{{dependencies.api.url}}",
		"API_TOKEN": "{{dependencies.api.token}}",
		"DB_HOST":   "{{dependencies.db.host}}",
	}).OverridableConfig.ServiceConfig
	inputs := map[string]api.DependencyInput{
		"api": {Managed: false, Values: map[string]any{"url": "http://api"}},
		"db":  {Managed: true},
	}
	got := validationErrors(t, verifyUnmanagedReferences(&cfg, inputs))
	if len(got) != 1 || !strings.Contains(got[0].Message, "{{dependencies.api.token}}") {
		t.Errorf("expected only api.token rejected; a managed dependency's outputs are not known yet. Got %+v", got)
	}
}

func TestVerifyUnmanagedReferences_InTheCommand(t *testing.T) {
	cfg := withCommand(submitRequest(nil, nil), "serve", "--api={{dependencies.api.url}}").OverridableConfig.ServiceConfig
	inputs := map[string]api.DependencyInput{"api": {Managed: false, Values: map[string]any{"host": "api"}}}
	want := []api.ValidationError{{Component: "config", Field: "command[1]",
		Message: `template variable {{dependencies.api.url}}: the input of unmanaged dependency "api" has no "url"`}}
	if got := validationErrors(t, verifyUnmanagedReferences(&cfg, inputs)); !slices.Equal(got, want) {
		t.Errorf("expected %+v, got %+v", want, got)
	}
}
