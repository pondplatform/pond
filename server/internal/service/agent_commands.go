package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// AgentCommands serves the agents: it hands out commands that are ready to
// run, tells an agent when to stop one, and records what comes back.
type AgentCommands struct {
	repos   store.Repos
	tx      store.Transactor
	machine *stateMachine
	log     *slog.Logger
	now     func() time.Time
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
