package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
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

// AgentCommands serves each cluster's one agent: it settles what the agent
// holds when it connects, hands out commands that are ready to run, tells the
// agent when to stop one, and records what comes back.
type AgentCommands struct {
	repos   store.Repos
	tx      store.Transactor
	machine *stateMachine
	log     *slog.Logger
	now     func() time.Time
}

// agentRestarted is the error of a command its agent was running when it
// restarted.
const agentRestarted = "the agent restarted while running this command, so its outcome is unknown"

// Connected settles the commands dispatched on the cluster when its agent
// connects, from what the agent says it holds: the command it runs, and those
// whose results it sends next. A cluster has one agent, so a command it does
// not hold is lost. When the agent restarted, it may have been running it, and
// the command fails, through Record like a result from the agent. Otherwise
// the agent never received it, and it is queued again.
//
// Connected can run again for the same hello, when the connection drops
// before the agent hears back.
func (a *AgentCommands) Connected(ctx context.Context, clusterID uuid.UUID, restarted bool, held []uuid.UUID) error {
	dispatched, err := a.repos.Deployments.ListDispatchedByCluster(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("list dispatched commands: %w", err)
	}
	for _, cmd := range dispatched {
		if slices.Contains(held, cmd.ID) {
			continue
		}
		if restarted {
			a.log.Warn("failing a command the agent was running when it restarted", "command_id", cmd.ID, "cluster_id", clusterID)
			if err := a.Record(ctx, clusterID, CommandResult{CommandID: cmd.ID, Error: agentRestarted}); err != nil {
				return err
			}
			continue
		}
		a.log.Warn("requeueing a command the agent never received", "command_id", cmd.ID, "cluster_id", clusterID)
		if err := a.requeue(ctx, cmd); err != nil {
			return err
		}
	}
	return nil
}

// requeue puts a command the agent never received back in the queue, where
// its age makes it the next to run. A command whose deployment has ended in
// the meantime is cancelled instead, like the queued commands of a deployment
// that ends.
func (a *AgentCommands) requeue(ctx context.Context, cmd *db.Command) error {
	err := a.tx.RunInTx(ctx, func(ctx context.Context, tx store.Repos) error {
		deployment, err := tx.Deployments.LockDeployment(ctx, cmd.DeploymentID)
		if err != nil {
			return err
		}
		lost, err := tx.Deployments.GetCommand(ctx, cmd.ID)
		if err != nil {
			return err
		}
		if lost.Status != db.CommandStatusDispatched {
			return nil
		}
		lost.Status = db.CommandStatusQueued
		if deployment.Status != db.DeploymentInProgress {
			lost.Status = db.CommandStatusCancelled
		}
		lost.UpdatedAt = a.now()
		return tx.Deployments.UpdateCommand(ctx, lost)
	})
	if err != nil {
		return fmt.Errorf("requeue command %s: %w", cmd.ID, err)
	}
	return nil
}

// Next dispatches the cluster's next queued command, or returns nil when
// there is none.
func (a *AgentCommands) Next(ctx context.Context, clusterID uuid.UUID) (*db.Command, error) {
	return a.repos.Deployments.ClaimNextCommand(ctx, clusterID, a.now())
}

// ShouldStop reports whether the agent running the command should cancel it:
// its deployment has ended, by a cancel or a failure elsewhere.
func (a *AgentCommands) ShouldStop(ctx context.Context, commandID uuid.UUID) (bool, error) {
	state, err := a.repos.Deployments.DeploymentStateOfCommand(ctx, commandID)
	if err != nil {
		return false, err
	}
	return state != db.DeploymentInProgress, nil
}

// Log stores a log line of a command running on the cluster.
func (a *AgentCommands) Log(ctx context.Context, clusterID, commandID uuid.UUID, line string) error {
	return a.repos.Deployments.AppendLog(ctx, clusterID, commandID, line)
}

// Record stores a command's result, then lets the state machine move the
// deployment on, in one transaction. A result for another cluster's command,
// or for one that is not running, is ignored.
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
		finished, err := tx.Deployments.GetCommand(ctx, result.CommandID)
		if err != nil {
			return err
		}
		if finished.Status != db.CommandStatusDispatched {
			a.log.Warn("ignoring result for a command that is not running", "command_id", finished.ID, "status", finished.Status)
			return nil
		}
		applyResult(finished, result, deployment.Status != db.DeploymentInProgress, a.now())
		if err := tx.Deployments.UpdateCommand(ctx, finished); err != nil {
			return err
		}
		return a.machine.advance(ctx, tx, deployment)
	})
	if err != nil {
		return fmt.Errorf("record result of command %s: %w", result.CommandID, err)
	}
	return nil
}

// applyResult copies a result onto its command. When the deployment has
// already ended, its agent was told to stop, so a failure means the command
// was cancelled; a success means it finished first.
func applyResult(cmd *db.Command, result CommandResult, stopped bool, now time.Time) {
	switch {
	case result.Success:
		cmd.Status = db.CommandStatusSucceeded
		cmd.Output = result.Output
	case stopped:
		cmd.Status = db.CommandStatusCancelled
		cmd.Error = result.Error
	default:
		cmd.Status = db.CommandStatusFailed
		cmd.Error = result.Error
	}
	cmd.UpdatedAt = now
}
