package api

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/service"
	shared "github.com/pondplatform/pond/shared/server/api"
)

type recordingSubmitter struct {
	service.DeploymentService
	got shared.SubmitRequest
}

func (r *recordingSubmitter) Submit(_ context.Context, req shared.SubmitRequest) (*domain.Deployment, error) {
	r.got = req
	return &domain.Deployment{}, nil
}

func TestSubmit_KeepsTheInputOutOfTheLog(t *testing.T) {
	var logs bytes.Buffer
	svc := &recordingSubmitter{}
	h := NewDeploymentHandler(svc, nil, nil, slog.New(slog.NewTextHandler(&logs, nil)))

	req := shared.SubmitRequest{EnvironmentName: "staging", DeploymentInput: shared.DeploymentInput{
		Dependencies: map[string]shared.DependencyInput{"db": {Values: map[string]any{"password": "db-secret"}}},
		Variables:    map[string]string{"SIGNING_KEY": "variable-secret"},
	}}
	if _, err := h.submit(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(logs.String(), "staging") {
		t.Errorf("expected the request logged, got %q", logs.String())
	}
	for _, secret := range []string{"db-secret", "variable-secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the log carries %q: %s", secret, logs.String())
		}
	}
	if svc.got.Variables["SIGNING_KEY"] != "variable-secret" || svc.got.Dependencies["db"].Values["password"] != "db-secret" {
		t.Errorf("the service should still get the whole input, got %+v", svc.got.DeploymentInput)
	}
}
