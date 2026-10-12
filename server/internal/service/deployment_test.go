package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/dependency"
	"github.com/pondplatform/pond/server/internal/helmgen"
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
	log := slog.New(slog.DiscardHandler)
	return &deploymentService{
		repos:    repos,
		tx:       inlineTx{repos},
		specs:    dependency.NewSpecRegistry(),
		resolver: config.NewResolver(),
		orchestrator: &orchestrator{
			repos:   repos,
			tx:      inlineTx{repos},
			helmGen: helmgen.NewGenerator(),
			topics:  &testutil.MockTopics{},
			log:     log,
			now:     time.Now,
		},
		log: log,
		now: time.Now,
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
		"app.env": {Format: ".env", MountDir: "/etc/app", Values: values},
	}
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
		"missing input": {
			req:  submitRequest(postgresDep, map[string]api.DependencyInput{}),
			want: []api.ValidationError{{Component: "dependency", Field: "db", Message: "missing input"}},
		},
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
	if _, err := s.Submit(context.Background(), submitRequest(postgresDep, map[string]api.DependencyInput{})); err == nil {
		t.Fatal("expected a validation error")
	}
}

func lockedDeployment(status db.DeploymentStatus) *testutil.MockDeploymentInfoStore {
	return &testutil.MockDeploymentInfoStore{
		LockDeploymentFn: func(_ context.Context, id uuid.UUID) (*db.Deployment, error) {
			return &db.Deployment{
				ID:     id,
				Status: status,
				ServiceConfigSnapshot: serviceconfig.ServiceConfig{
					Dependencies: postgresDep,
				},
			}, nil
		},
	}
}

func TestConfigureDeployment_RejectsUndeclaredInputsBeforeWriting(t *testing.T) {
	deployments := lockedDeployment(api.DeploymentStatusAwaitingInput)
	deployments.UpdateDepConfigFn = func(context.Context, *db.DependencyDeployment) error {
		t.Error("no dependency should be written when inputs are rejected")
		return nil
	}
	inputs := map[string]api.DependencyInput{"db": {Managed: true}, "nope": {Managed: true}}
	err := newTestService(deployments).ConfigureDeployment(context.Background(), uuid.New(), inputs)
	validationErrors(t, err)
}

func TestConfigureDeployment_UnknownDeploymentIsNotFound(t *testing.T) {
	deployments := &testutil.MockDeploymentInfoStore{
		LockDeploymentFn: func(context.Context, uuid.UUID) (*db.Deployment, error) {
			return nil, api.NotFound("deployment not found")
		},
	}
	err := newTestService(deployments).ConfigureDeployment(context.Background(), uuid.New(), nil)
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestConfigureDeployment_ConflictsOnceFinishedOrRollingOut(t *testing.T) {
	for _, status := range []db.DeploymentStatus{api.DeploymentStatusSucceeded, api.DeploymentStatusFailed, api.DeploymentStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			err := newTestService(lockedDeployment(status)).ConfigureDeployment(context.Background(), uuid.New(), nil)
			if !errors.Is(err, api.ErrConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
		})
	}
	t.Run("helm started", func(t *testing.T) {
		deployments := lockedDeployment(api.DeploymentStatusPending)
		lock := deployments.LockDeploymentFn
		deployments.LockDeploymentFn = func(ctx context.Context, id uuid.UUID) (*db.Deployment, error) {
			d, err := lock(ctx, id)
			helm := uuid.New()
			d.HelmCommandID = &helm
			return d, err
		}
		err := newTestService(deployments).ConfigureDeployment(context.Background(), uuid.New(), nil)
		if !errors.Is(err, api.ErrConflict) {
			t.Fatalf("expected conflict, got %v", err)
		}
	})
}

func TestCancel_CancelsTheDeploymentAndItsLiveCommands(t *testing.T) {
	for _, status := range []db.DeploymentStatus{api.DeploymentStatusPending, api.DeploymentStatusAwaitingInput} {
		t.Run(string(status), func(t *testing.T) {
			deployments := lockedDeployment(status)
			var saved *db.Deployment
			deployments.SaveDeploymentFn = func(_ context.Context, d *db.Deployment) error {
				saved = d
				return nil
			}
			var to db.CommandStatus
			var from []db.CommandStatus
			deployments.UpdateCommandsByDeploymentFn = func(_ context.Context, _ uuid.UUID, t db.CommandStatus, f ...db.CommandStatus) error {
				to, from = t, f
				return nil
			}

			if err := newTestService(deployments).Cancel(context.Background(), uuid.New()); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if saved == nil || saved.Status != api.DeploymentStatusCancelled || saved.CompletedAt == nil {
				t.Errorf("expected the deployment saved as cancelled with completedAt, got %+v", saved)
			}
			if to != db.CommandStatusCancelled || len(from) != 2 || from[0] != db.CommandStatusQueued || from[1] != db.CommandStatusDispatched {
				t.Errorf("expected queued and dispatched commands cancelled, got %v -> %v", from, to)
			}
		})
	}
}

func TestCancel_ConflictsOnceFinished(t *testing.T) {
	for _, status := range []db.DeploymentStatus{api.DeploymentStatusSucceeded, api.DeploymentStatusFailed, api.DeploymentStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			deployments := lockedDeployment(status)
			deployments.SaveDeploymentFn = func(context.Context, *db.Deployment) error {
				t.Error("a finished deployment must not be written")
				return nil
			}
			if err := newTestService(deployments).Cancel(context.Background(), uuid.New()); !errors.Is(err, api.ErrConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
		})
	}
}
