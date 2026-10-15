package service

import (
	"context"
	"errors"
	"log/slog"
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
		ImageTag:     "v1",
		TriggeredBy:  "user",
		Dependencies: inputs,
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
		"DB_HOST": "{{db.host}}",
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
				Message: "template variable {{cache.url}} names no declared dependency and no known value (service.port, deployment.replicas, env.<NAME>)"}},
		},
		"template variable naming nothing inside a list": {
			req: withConfigValues(submitRequest(postgresDep, nil), map[string]any{"URLS": []any{"{{db.host}}", "{{cache.url}}"}}),
			want: []api.ValidationError{{Component: "config", Field: "app.env",
				Message: "template variable {{cache.url}} names no declared dependency and no known value (service.port, deployment.replicas, env.<NAME>)"}},
		},
		"template variable naming nothing in the command": {
			req: withCommand(submitRequest(postgresDep, nil), "serve", "--cache={{cache.url}}"),
			want: []api.ValidationError{{Component: "config", Field: "command[1]",
				Message: "template variable {{cache.url}} names no declared dependency and no known value (service.port, deployment.replicas, env.<NAME>)"}},
		},
		"sensitive output in the command": {
			req: withCommand(submitRequest(postgresDep, nil), "serve", "--db", "postgres://{{db.username}}:{{db.password}}@{{db.host}}"),
			want: []api.ValidationError{{Component: "config", Field: "command[2]",
				Message: "template variable {{db.password}} is sensitive, and arguments are visible to anyone who can read the workload; pass it in a config file instead"}},
		},
		// An unmanaged input may carry any key, and nothing says whether it is a secret.
		"undeclared output in the command": {
			req: withCommand(submitRequest(map[string]serviceconfig.DependencyDeclaration{"api": {Type: "http-service"}}, nil), "--token={{api.token}}"),
			want: []api.ValidationError{{Component: "config", Field: "command[0]",
				Message: `template variable {{api.token}}: http-service declares no "token" output, so it may be sensitive, and arguments are visible to anyone who can read the workload; pass it in a config file instead`}},
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

func TestValidateSubmission_AcceptsReferencesToOwnValues(t *testing.T) {
	s := newTestService(&testutil.MockDeploymentInfoStore{})
	req := withConfigValues(submitRequest(nil, nil), map[string]any{
		"PORT":  "{{service.port}}",
		"LEVEL": "{{env.LOG_LEVEL}}",
	})
	req.OverridableConfig.ServiceConfig.Service = &serviceconfig.ServiceSpec{Port: serviceconfig.Ptr(int32(8080))}
	req.OverridableConfig.ServiceConfig.Env = map[string]string{"LOG_LEVEL": "debug"}
	if _, _, err := s.validateSubmission(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSubmission_AcceptsNonSensitiveReferencesInTheCommand(t *testing.T) {
	s := newTestService(&testutil.MockDeploymentInfoStore{})
	deps := map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}, "api": {Type: "http-service"}}
	req := withCommand(submitRequest(deps, nil),
		"serve", "--port", "{{service.port}}", "--db={{db.host}}:{{db.port}}", "--api", "{{api.url}}", "--log-level", "{{env.LOG_LEVEL}}")
	req.OverridableConfig.ServiceConfig.Service = &serviceconfig.ServiceSpec{Port: serviceconfig.Ptr(int32(8080))}
	req.OverridableConfig.ServiceConfig.Env = map[string]string{"LOG_LEVEL": "debug"}
	if _, _, err := s.validateSubmission(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
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

func TestSubmit_VolumesNeverShrink(t *testing.T) {
	data := func(size string) map[string]serviceconfig.VolumeSpec {
		return map[string]serviceconfig.VolumeSpec{"data": {Type: "shared-single-node", MountPath: "/data", Size: size}}
	}
	succeeded := func(volumes map[string]serviceconfig.VolumeSpec) []db.Deployment {
		return []db.Deployment{{
			Status:                db.DeploymentSucceeded,
			ServiceConfigSnapshot: serviceconfig.ServiceConfig{Volumes: volumes},
		}}
	}

	cases := map[string]struct {
		lastSucceeded []db.Deployment
		volumes       map[string]serviceconfig.VolumeSpec
		wantRejected  bool
	}{
		"smaller than in the last succeeded deployment": {succeeded(data("10Gi")), data("5Gi"), true},
		"the same size in another unit":                 {succeeded(data("1Gi")), data("1024Mi"), false},
		"grown":                                         {succeeded(data("1Gi")), data("2Gi"), false},
		"a volume the last deployment didn't have":      {succeeded(nil), data("1Gi"), false},
		"the first deployment":                          {nil, data("1Gi"), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &db.Service{ID: uuid.New(), ProjectID: testProjectID, Name: "my-service"}
			env := &db.Environment{ID: uuid.New(), Name: "staging"}
			created := false
			deployments := &testutil.MockDeploymentInfoStore{
				ListByServiceFilteredFn: func(_ context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *api.DeploymentStatus, limit int, cursor string) ([]db.Deployment, error) {
					// Only this environment's succeeded deployments count, so a
					// failed grow can be reverted.
					if serviceID != svc.ID || environmentID == nil || *environmentID != env.ID ||
						status == nil || *status != api.DeploymentStatusSucceeded || limit != 1 || cursor != "" {
						t.Errorf("unexpected lookup: service %s, environment %v, status %v, limit %d, cursor %q", serviceID, environmentID, status, limit, cursor)
					}
					return tc.lastSucceeded, nil
				},
				CreateFn: func(context.Context, *db.Deployment) error {
					created = true
					return nil
				},
			}
			s := newTestService(deployments)
			s.repos.Services.(*testutil.MockServiceRepository).GetByNameFn = func(context.Context, uuid.UUID, string) (*db.Service, error) {
				return svc, nil
			}
			envs := s.repos.Envs.(*testutil.MockEnvironmentRepository)
			envs.GetByNameFn = func(context.Context, uuid.UUID, string) (*db.Environment, error) { return env, nil }
			envs.GetByIDFn = func(context.Context, uuid.UUID) (*db.Environment, error) { return env, nil }

			req := submitRequest(nil, nil)
			req.OverridableConfig.ServiceConfig.Volumes = tc.volumes
			_, err := s.Submit(context.Background(), req)

			if !tc.wantRejected {
				if err != nil || !created {
					t.Fatalf("expected the deployment to be created, got %v", err)
				}
				return
			}
			want := []api.ValidationError{{Component: "config", Field: `volumes["data"].size`,
				Message: "must be at least 10Gi, its size in the last successful deployment: a volume can grow but never shrink, got 5Gi"}}
			if got := validationErrors(t, err); !slices.Equal(got, want) {
				t.Errorf("expected %+v, got %+v", want, got)
			}
			if created {
				t.Error("no deployment should be created when a volume shrinks")
			}
		})
	}
}

func TestVerifyUnmanagedReferences(t *testing.T) {
	cfg := withConfigValues(submitRequest(nil, nil), map[string]any{
		"API_URL":   "{{api.url}}",
		"API_TOKEN": "{{api.token}}",
		"DB_HOST":   "{{db.host}}",
	}).OverridableConfig.ServiceConfig
	inputs := map[string]api.DependencyInput{
		"api": {Managed: false, Values: map[string]any{"url": "http://api"}},
		"db":  {Managed: true},
	}
	got := validationErrors(t, verifyUnmanagedReferences(&cfg, inputs))
	if len(got) != 1 || !strings.Contains(got[0].Message, "{{api.token}}") {
		t.Errorf("expected only api.token rejected; a managed dependency's outputs are not known yet. Got %+v", got)
	}
}

func TestVerifyUnmanagedReferences_InTheCommand(t *testing.T) {
	cfg := withCommand(submitRequest(nil, nil), "serve", "--api={{api.url}}").OverridableConfig.ServiceConfig
	inputs := map[string]api.DependencyInput{"api": {Managed: false, Values: map[string]any{"host": "api"}}}
	want := []api.ValidationError{{Component: "config", Field: "command[1]",
		Message: `template variable {{api.url}}: the input of unmanaged dependency "api" has no "url"`}}
	if got := validationErrors(t, verifyUnmanagedReferences(&cfg, inputs)); !slices.Equal(got, want) {
		t.Errorf("expected %+v, got %+v", want, got)
	}
}
