package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

func TestTimeOutCommand_LeavesACommandThatMovedOn(t *testing.T) {
	listed := &db.Command{ID: uuid.New(), DeploymentID: uuid.New(), Status: db.CommandStatusDispatched, UpdatedAt: time.Now().Add(-time.Hour)}
	cases := map[string]db.Command{
		"result arrived":   {ID: listed.ID, Status: db.CommandStatusSucceeded, UpdatedAt: time.Now()},
		"dispatched again": {ID: listed.ID, Status: db.CommandStatusDispatched, UpdatedAt: time.Now()},
		"cancelled":        {ID: listed.ID, Status: db.CommandStatusCancelled, UpdatedAt: listed.UpdatedAt},
	}
	for name, current := range cases {
		t.Run(name, func(t *testing.T) {
			deployments := lockedDeployment(api.DeploymentStatusPending)
			deployments.GetCommandFn = func(context.Context, uuid.UUID) (*db.Command, error) {
				cmd := current
				return &cmd, nil
			}
			deployments.UpdateCommandFn = func(context.Context, *db.Command) error {
				t.Error("a command that moved on must not be timed out")
				return nil
			}
			if err := newTestService(deployments).orchestrator.TimeOutCommand(context.Background(), listed, "timed out"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestFail_LeavesAFinishedDeployment(t *testing.T) {
	deployments := lockedDeployment(api.DeploymentStatusSucceeded)
	deployments.SaveDeploymentFn = func(context.Context, *db.Deployment) error {
		t.Error("a finished deployment must not be failed")
		return nil
	}
	if err := newTestService(deployments).orchestrator.Fail(context.Background(), uuid.New(), "stalled"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFail_FailsAndCancelsLiveCommands(t *testing.T) {
	deployments := lockedDeployment(api.DeploymentStatusPending)
	var saved *db.Deployment
	deployments.SaveDeploymentFn = func(_ context.Context, d *db.Deployment) error {
		saved = d
		return nil
	}
	cancelled := false
	deployments.UpdateCommandsByDeploymentFn = func(_ context.Context, _ uuid.UUID, to db.CommandStatus, _ ...db.CommandStatus) error {
		cancelled = to == db.CommandStatusCancelled
		return nil
	}
	if err := newTestService(deployments).orchestrator.Fail(context.Background(), uuid.New(), "stalled"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saved == nil || saved.Status != api.DeploymentStatusFailed || saved.Error == nil || *saved.Error != "stalled" || saved.CompletedAt == nil {
		t.Errorf("expected failed with the reason and completedAt, got %+v", saved)
	}
	if !cancelled {
		t.Error("expected live commands to be cancelled")
	}
}

func TestSweepConfig_ZeroFieldsTakeDefaults(t *testing.T) {
	cfg := SweepConfig{CommandTimeout: 90 * time.Minute}.withDefaults()
	if cfg.Interval != 15*time.Second || cfg.CommandTimeout != 90*time.Minute || cfg.StallTimeout != 10*time.Minute {
		t.Errorf("unexpected config %+v", cfg)
	}
}
