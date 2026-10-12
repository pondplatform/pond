package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
)

// CommandResult is what an agent reports when it finishes a command.
type CommandResult struct {
	CommandID uuid.UUID
	Success   bool
	Output    json.RawMessage
	Error     string
}

// AgentCommands serves the agents: it hands out commands that are ready to
// run and records what comes back.
type AgentCommands struct {
	repos   store.Repos
	tx      store.Transactor
	helmGen helmgen.HelmValuesGenerator
	log     *slog.Logger
	now     func() time.Time
}

// Next dispatches the cluster's next command that is ready to run, or returns
// nil when there is none.
func (a *AgentCommands) Next(ctx context.Context, clusterID uuid.UUID) (*db.Command, error) {
	return a.repos.Deployments.ClaimNextCommand(ctx, clusterID, a.now())
}

// Log stores a log line of a command running on the cluster.
func (a *AgentCommands) Log(ctx context.Context, clusterID, commandID uuid.UUID, line string) error {
	return a.repos.Deployments.AppendLog(ctx, clusterID, commandID, line)
}

// Record stores a command's result and moves its deployment on, in one
// transaction: a failure fails the deployment, and a finished phase starts the
// next one or completes the deployment. A result for another cluster's
// command, or one that was already recorded, is ignored.
func (a *AgentCommands) Record(ctx context.Context, clusterID uuid.UUID, result CommandResult) error {
	cmd, err := a.repos.Deployments.GetCommand(ctx, result.CommandID)
	if errors.Is(err, api.ErrNotFound) || (err == nil && cmd.ClusterID != clusterID) {
		a.log.Warn("ignoring result for a command the agent's cluster does not have", "command_id", result.CommandID, "cluster_id", clusterID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("get command: %w", err)
	}

	err = a.tx.RunInTx(ctx, func(ctx context.Context, tx store.Repos) error {
		deployment, err := tx.Deployments.LockDeployment(ctx, cmd.DeploymentID)
		if err != nil {
			return err
		}
		commands, err := tx.Deployments.ListCommandsByDeployment(ctx, deployment.ID)
		if err != nil {
			return fmt.Errorf("list commands: %w", err)
		}
		finished := commandByID(commands, &result.CommandID)
		if finished.Status != db.CommandStatusDispatched && finished.Status != db.CommandStatusCancelled {
			a.log.Warn("ignoring result for a command that is not running", "command_id", finished.ID, "status", finished.Status)
			return nil
		}
		applyResult(finished, result, a.now())
		if err := tx.Deployments.UpdateCommand(ctx, finished); err != nil {
			return err
		}
		if deployment.Status != db.DeploymentInProgress {
			return nil
		}
		return a.advance(ctx, tx, deployment, commands, finished)
	})
	if err != nil {
		return fmt.Errorf("record result of command %s: %w", result.CommandID, err)
	}
	return nil
}

func (a *AgentCommands) advance(ctx context.Context, tx store.Repos, deployment *db.Deployment, commands []*db.Command, finished *db.Command) error {
	dependencies, err := tx.Deployments.ListDepConfigs(ctx, deployment.ID)
	if err != nil {
		return fmt.Errorf("list dependencies: %w", err)
	}
	next := afterResult(finished, commands, dependencies)
	switch {
	case next.failure != "":
		return finish(ctx, tx, deployment, db.DeploymentFailed, next.failure, a.now())
	case next.done:
		if err := finish(ctx, tx, deployment, db.DeploymentSucceeded, "", a.now()); err != nil {
			return err
		}
		return tx.Services.SetRunningDeployment(ctx, deployment.ServiceID, deployment.ID)
	case next.start != nil:
		env, err := tx.Envs.GetByID(ctx, deployment.EnvironmentID)
		if err != nil {
			return fmt.Errorf("get environment: %w", err)
		}
		changed, err := activate(*deployment, *env, dependencies, commands, *next.start, a.helmGen)
		if err != nil {
			return finish(ctx, tx, deployment, db.DeploymentFailed, err.Error(), a.now())
		}
		for _, cmd := range changed {
			cmd.UpdatedAt = a.now()
			if err := tx.Deployments.UpdateCommand(ctx, cmd); err != nil {
				return err
			}
		}
	}
	return nil
}

// finish ends the deployment. Commands that have not run yet are cancelled,
// and so is one that is running: its late result is still recorded.
func finish(ctx context.Context, tx store.Repos, deployment *db.Deployment, state db.DeploymentState, reason string, now time.Time) error {
	deployment.Status = state
	deployment.CompletedAt = &now
	if reason != "" {
		deployment.Error = &reason
	}
	if err := tx.Deployments.SaveDeployment(ctx, deployment); err != nil {
		return err
	}
	if err := tx.Deployments.UpdateCommandsByDeployment(ctx, deployment.ID,
		db.CommandStatusCancelled, db.CommandStatusQueued, db.CommandStatusDispatched); err != nil {
		return fmt.Errorf("cancel remaining commands: %w", err)
	}
	return nil
}

// applyResult copies a result onto its command. A cancelled command stays
// cancelled, but keeps what the agent reported.
func applyResult(cmd *db.Command, result CommandResult, now time.Time) {
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
