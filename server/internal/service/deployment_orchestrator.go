package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
)

// orchestrator is the only writer of deployment progress. Events such as a
// command result are recorded first; Reconcile then decides what follows.
// Every step locks the deployment row, so steps for one deployment never
// interleave, and Reconcile is safe to call again at any time.
type orchestrator struct {
	repos   store.Repos
	tx      store.Transactor
	helmGen helmgen.HelmValuesGenerator
	topics  events.Topics
	log     *slog.Logger
	now     func() time.Time
}

// Reconcile moves the deployment as far forward as its state allows and
// returns it as stored. An error means nothing was written; calling it again
// retries.
func (o *orchestrator) Reconcile(ctx context.Context, deploymentID uuid.UUID) (*db.Deployment, error) {
	var result decision
	err := o.tx.RunInTx(ctx, func(ctx context.Context, tx store.Repos) error {
		state, err := loadState(ctx, tx, deploymentID)
		if err != nil {
			return err
		}
		result = decide(*state, o.helmGen, o.now())
		return persist(ctx, tx, result)
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile deployment %s: %w", deploymentID, err)
	}
	for _, cmd := range result.Commands {
		// The command is committed, so an agent still finds it on its next
		// ready signal; the notification only wakes an idle one sooner.
		if err := notifyCommandQueued(ctx, o.topics, cmd); err != nil {
			o.log.Error("notify queued command", "command_id", cmd.ID, "err", err)
		}
	}
	return &result.Deployment, nil
}

// RecordResult stores an agent's command result, then reconciles the
// command's deployment. A result for a command that already has one is
// ignored, so a redelivered result is harmless.
func (o *orchestrator) RecordResult(ctx context.Context, result events.CommandResult) error {
	cmd, err := o.repos.Deployments.GetCommand(ctx, result.CommandID)
	if errors.Is(err, api.ErrNotFound) {
		o.log.Warn("result for unknown command", "command_id", result.CommandID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("get command: %w", err)
	}

	err = o.tx.RunInTx(ctx, func(ctx context.Context, tx store.Repos) error {
		if _, err := tx.Deployments.LockDeployment(ctx, cmd.DeploymentID); err != nil {
			return err
		}
		locked, err := tx.Deployments.GetCommand(ctx, result.CommandID)
		if err != nil {
			return err
		}
		if locked.Status == db.CommandStatusSucceeded || locked.Status == db.CommandStatusFailed {
			o.log.Warn("ignoring duplicate command result", "command_id", locked.ID, "status", locked.Status)
			return nil
		}
		applyResult(locked, result, o.now())
		return tx.Deployments.UpdateCommand(ctx, locked)
	})
	if err != nil {
		return fmt.Errorf("record result of command %s: %w", result.CommandID, err)
	}
	_, err = o.Reconcile(ctx, cmd.DeploymentID)
	return err
}

// TimeOutCommand fails a command that is still dispatched, unchanged since it
// was listed, then reconciles its deployment. A result that arrived in the
// meantime wins.
func (o *orchestrator) TimeOutCommand(ctx context.Context, cmd *db.Command, reason string) error {
	timedOut := false
	err := o.tx.RunInTx(ctx, func(ctx context.Context, tx store.Repos) error {
		if _, err := tx.Deployments.LockDeployment(ctx, cmd.DeploymentID); err != nil {
			return err
		}
		locked, err := tx.Deployments.GetCommand(ctx, cmd.ID)
		if err != nil {
			return err
		}
		if locked.Status != db.CommandStatusDispatched || !locked.UpdatedAt.Equal(cmd.UpdatedAt) {
			return nil
		}
		applyResult(locked, events.CommandResult{CommandID: cmd.ID, Error: reason}, o.now())
		timedOut = true
		return tx.Deployments.UpdateCommand(ctx, locked)
	})
	if err != nil {
		return fmt.Errorf("time out command %s: %w", cmd.ID, err)
	}
	if !timedOut {
		return nil
	}
	_, err = o.Reconcile(ctx, cmd.DeploymentID)
	return err
}

// Fail ends a deployment that is not finished yet, with reason.
func (o *orchestrator) Fail(ctx context.Context, deploymentID uuid.UUID, reason string) error {
	err := o.tx.RunInTx(ctx, func(ctx context.Context, tx store.Repos) error {
		deployment, err := tx.Deployments.LockDeployment(ctx, deploymentID)
		if err != nil {
			return err
		}
		if isTerminal(deployment.Status) {
			return nil
		}
		d := decision{Deployment: *deployment}
		d.fail(reason, o.now())
		return persist(ctx, tx, d)
	})
	if err != nil {
		return fmt.Errorf("fail deployment %s: %w", deploymentID, err)
	}
	return nil
}

// applyResult copies a result onto its command. A cancelled command stays
// cancelled, but keeps what the agent reported.
func applyResult(cmd *db.Command, result events.CommandResult, now time.Time) {
	if result.Success {
		cmd.Output = result.Output
	} else {
		cmd.Error = result.Error
	}
	if cmd.Status != db.CommandStatusCancelled {
		if result.Success {
			cmd.Status = db.CommandStatusSucceeded
		} else {
			cmd.Status = db.CommandStatusFailed
		}
	}
	cmd.UpdatedAt = now
}

func loadState(ctx context.Context, tx store.Repos, deploymentID uuid.UUID) (*deploymentState, error) {
	deployment, err := tx.Deployments.LockDeployment(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	env, err := tx.Envs.GetByID(ctx, deployment.EnvironmentID)
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	dependencies, err := tx.Deployments.ListDepConfigs(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("list dependencies: %w", err)
	}
	commands, err := tx.Deployments.ListCommandsByDeployment(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	byID := make(map[uuid.UUID]*db.Command, len(commands))
	for _, cmd := range commands {
		byID[cmd.ID] = cmd
	}
	return &deploymentState{
		Deployment:   *deployment,
		Environment:  *env,
		Dependencies: dependencies,
		Commands:     byID,
	}, nil
}

// persist writes a decision. Commands come first, so no row ever points at a
// command that does not exist yet.
func persist(ctx context.Context, tx store.Repos, d decision) error {
	for _, cmd := range d.Commands {
		if err := tx.Deployments.CreateCommand(ctx, cmd); err != nil {
			return fmt.Errorf("create %s command: %w", cmd.Type, err)
		}
	}
	for i := range d.Dependencies {
		if err := tx.Deployments.UpdateDepConfig(ctx, &d.Dependencies[i]); err != nil {
			return fmt.Errorf("update dependency %q: %w", d.Dependencies[i].DependencyName, err)
		}
	}
	if d.CancelLive {
		if err := tx.Deployments.UpdateCommandsByDeployment(ctx, d.Deployment.ID,
			db.CommandStatusCancelled, db.CommandStatusQueued, db.CommandStatusDispatched); err != nil {
			return fmt.Errorf("cancel live commands: %w", err)
		}
	}
	if d.DeploymentChanged {
		if err := tx.Deployments.SaveDeployment(ctx, &d.Deployment); err != nil {
			return err
		}
	}
	if d.SetServiceRunning {
		if err := tx.Services.SetRunningDeployment(ctx, d.Deployment.ServiceID, d.Deployment.ID); err != nil {
			return err
		}
	}
	return nil
}
