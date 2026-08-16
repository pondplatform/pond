package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/testutil"
	"github.com/pondplatform/pond/shared/server/api"
)

func TestCancel_HappyPath(t *testing.T) {
	ctx := context.Background()
	depID := uuid.New()

	var capturedStatus domain.DeploymentStatus
	var cancelledFromStatus domain.CommandStatus
	var cancelledToStatus domain.CommandStatus

	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{ID: depID, Status: api.DeploymentStatusPending}, nil
		},
		UpdateStatusFn: func(_ context.Context, _ uuid.UUID, status domain.DeploymentStatus, completedAt *time.Time) error {
			capturedStatus = status
			if completedAt == nil {
				t.Error("expected non-nil completedAt on cancel")
			}
			return nil
		},
		UpdateCommandsByDeploymentFn: func(_ context.Context, _ uuid.UUID, from, to domain.CommandStatus) error {
			cancelledFromStatus = from
			cancelledToStatus = to
			return nil
		},
	}

	svc := &deploymentService{deploymentInfo: store, log: slog.Default()}
	if err := svc.Cancel(ctx, depID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedStatus != api.DeploymentStatusCancelled {
		t.Errorf("expected cancelled status, got %s", capturedStatus)
	}
	if cancelledFromStatus != domain.CommandStatusQueued {
		t.Errorf("expected from=queued, got %s", cancelledFromStatus)
	}
	if cancelledToStatus != domain.CommandStatusCancelled {
		t.Errorf("expected to=cancelled, got %s", cancelledToStatus)
	}
}

func TestCancel_AlreadySucceededReturnsError(t *testing.T) {
	ctx := context.Background()
	depID := uuid.New()

	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{ID: depID, Status: api.DeploymentStatusSucceeded}, nil
		},
	}

	svc := &deploymentService{deploymentInfo: store, log: slog.Default()}
	err := svc.Cancel(ctx, depID)
	if err == nil {
		t.Fatal("expected error when cancelling a succeeded deployment")
	}
}

func TestCancel_AlreadyFailedReturnsError(t *testing.T) {
	ctx := context.Background()
	depID := uuid.New()

	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{ID: depID, Status: api.DeploymentStatusFailed}, nil
		},
	}

	svc := &deploymentService{deploymentInfo: store, log: slog.Default()}
	err := svc.Cancel(ctx, depID)
	if err == nil {
		t.Fatal("expected error when cancelling a failed deployment")
	}
}

func TestCancel_AwaitingInputCanBeCancelled(t *testing.T) {
	ctx := context.Background()
	depID := uuid.New()

	store := &testutil.MockDeploymentInfoStore{
		GetByIDFn: func(_ context.Context, _ uuid.UUID) (*domain.Deployment, error) {
			return &domain.Deployment{ID: depID, Status: api.DeploymentStatusAwaitingInput}, nil
		},
		UpdateStatusFn:               func(_ context.Context, _ uuid.UUID, _ domain.DeploymentStatus, _ *time.Time) error { return nil },
		UpdateCommandsByDeploymentFn: func(_ context.Context, _ uuid.UUID, _, _ domain.CommandStatus) error { return nil },
	}

	svc := &deploymentService{deploymentInfo: store, log: slog.Default()}
	if err := svc.Cancel(ctx, depID); err != nil {
		t.Fatalf("expected awaiting_input deployment to be cancellable, got: %v", err)
	}
}
