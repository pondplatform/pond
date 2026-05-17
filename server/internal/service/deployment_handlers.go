package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

// handleAgentReady dispatches the next queued command for the cluster.
func (s *deploymentService) handleAgentReady(ctx context.Context, e events.AgentReady) {
	cmds, err := s.deploymentInfo.ListQueuedCommandsByCluster(ctx, e.ClusterID)
	if err != nil {
		s.log.Error("list queued commands", "cluster_id", e.ClusterID, "err", err)
		return
	}
	if len(cmds) == 0 {
		return
	}
	cmd := cmds[0]
	s.log.Info("dispatching command to agent", "cluster_id", e.ClusterID, "command_id", cmd.ID, "type", cmd.Type)
	cmd.Status = domain.CommandStatusDispatched
	cmd.UpdatedAt = time.Now()
	if err := s.deploymentInfo.UpdateCommand(ctx, cmd); err != nil {
		s.log.Error("update command to dispatched", "command_id", cmd.ID, "err", err)
		return
	}
	s.bus.Publish(ctx, events.ClusterCommandDispatchTopic(e.ClusterID), events.CommandDispatch{Cmd: cmd})
}

// handleCommandStarted transitions a deployment from pending -> running when
// the agent acknowledges the command it just received.
func (s *deploymentService) handleCommandStarted(ctx context.Context, e events.CommandStarted) {
	dep, err := s.deploymentInfo.GetByID(ctx, e.DeploymentID)
	if err != nil {
		s.log.Error("get deployment for command_started", "deployment_id", e.DeploymentID, "err", err)
		return
	}
	if dep.Status != api.DeploymentStatusPending {
		return
	}
	s.log.Info("deployment running", "deployment_id", e.DeploymentID)
	if err := s.deploymentInfo.UpdateStatus(ctx, e.DeploymentID, api.DeploymentStatusRunning, nil); err != nil {
		s.log.Error("mark deployment running", "deployment_id", e.DeploymentID, "err", err)
	}
}

// handleCommandLog persists a streamed log line from a running command.
func (s *deploymentService) handleCommandLog(ctx context.Context, e events.CommandLog) {
	if err := s.deploymentInfo.AppendLog(ctx, e.CommandID, e.Line); err != nil {
		s.log.Error("append log", "command_id", e.CommandID, "err", err)
	}
}

// handleAgentDisconnected requeues the in-flight command (if any) so it can
// be redelivered to the next connected agent for the same cluster.
func (s *deploymentService) handleAgentDisconnected(ctx context.Context, e events.AgentDisconnected) {
	if e.InFlightCommandID == uuid.Nil {
		return
	}
	s.log.Info("agent disconnected with in-flight command, requeueing", "command_id", e.InFlightCommandID)
	cmd, err := s.deploymentInfo.GetCommand(ctx, e.InFlightCommandID)
	if err != nil {
		s.log.Error("get command on disconnect", "command_id", e.InFlightCommandID, "err", err)
		return
	}
	if cmd.Status != domain.CommandStatusDispatched {
		return // already completed or cancelled
	}
	cmd.Status = domain.CommandStatusQueued
	cmd.UpdatedAt = time.Now()
	if err := s.deploymentInfo.UpdateCommand(ctx, cmd); err != nil {
		s.log.Error("requeue command on disconnect", "command_id", e.InFlightCommandID, "err", err)
	}
}

// handleUserInputProvided checks if all dependencies now have their input provided.
// Only when ALL dependencies have input will it schedule ALL of them at once.
func (s *deploymentService) handleUserInputProvided(ctx context.Context, e events.UserInputProvided) {
	s.log.Info("user input provided", "deployment_id", e.DeploymentID)
	dep, err := s.deploymentInfo.GetByID(ctx, e.DeploymentID)
	if err != nil {
		s.log.Error("get deployment for user_input.provided", "deployment_id", e.DeploymentID, "err", err)
		return
	}
	env, err := s.envs.GetByID(ctx, dep.EnvironmentID)
	if err != nil {
		s.log.Error("get environment for deployment", "deployment_id", e.DeploymentID, "err", err)
		return
	}

	// Check if any dependencies still need input
	stillAwaiting, err := s.deploymentInfo.AnyDepConfigAwaitingInput(ctx, e.DeploymentID)
	if err != nil {
		s.log.Error("check awaiting input", "deployment_id", e.DeploymentID, "err", err)
		return
	}

	if stillAwaiting {
		s.log.Info("still awaiting input for other dependencies", "deployment_id", e.DeploymentID)
		return
	}

	s.log.Info("all inputs provided, scheduling dependencies", "deployment_id", e.DeploymentID)
	scheduledDeps, helmCmd, err := s.scheduleAllDepsAfterInput(ctx, dep, env)
	if err != nil {
		s.log.Error("schedule all deps after input", "deployment_id", e.DeploymentID, "err", err)
		return
	}

	// Publish CommandQueued events for all pending managed deps
	s.publishScheduledDepEvents(ctx, scheduledDeps, env.ClusterID, e.DeploymentID)

	// If all deps were non-managed, helm was enqueued directly — notify the agent
	if helmCmd != nil {
		s.bus.Publish(ctx, events.ClusterCommandQueuedTopic(helmCmd.ClusterID), events.CommandQueued{
			ClusterID:    helmCmd.ClusterID,
			CommandID:    helmCmd.ID,
			DeploymentID: e.DeploymentID,
		})
	}
}

// scheduleAllDepsAfterInput schedules all dependencies after user input is provided.
// Returns the list of scheduled dependencies for event publishing, and the helm
// command if one was enqueued (all-non-managed case).
func (s *deploymentService) scheduleAllDepsAfterInput(ctx context.Context, dep *domain.Deployment, env *domain.Environment) ([]domain.DependencyDeployment, *domain.Command, error) {
	var scheduledDeps []domain.DependencyDeployment
	var helmCmd *domain.Command

	err := s.tx.RunInTx(ctx, func(ctx context.Context, tx TxRepos) error {
		depConfigs, err := tx.DeploymentInfo.ListDepConfigs(ctx, dep.ID)
		if err != nil {
			return fmt.Errorf("list dep configs: %w", err)
		}

		for _, cfg := range depConfigs {
			// Skip deps that are not in a state where they need scheduling
			// (they might already be pending/succeeded from previous runs or non-managed)
			if cfg.Status != domain.DependencyDeploymentStatusAwaitingInput &&
				cfg.Status != domain.DependencyDeploymentStatusPending {
				// Non-awaiting, non-pending deps (e.g., succeeded) - keep track for helm check
				scheduledDeps = append(scheduledDeps, cfg)
				continue
			}

			// Schedule this dependency
			scheduled, schedErr := s.depSvc.ScheduleAfterInput(ctx, tx, dep, env, cfg.DependencyName)
			if schedErr != nil {
				return fmt.Errorf("schedule dep %q: %w", cfg.DependencyName, schedErr)
			}
			scheduledDeps = append(scheduledDeps, *scheduled)
		}

		// Check if all deps are already complete (all non-managed)
		allSucceeded := true
		for _, d := range scheduledDeps {
			if d.Status != domain.DependencyDeploymentStatusSucceeded {
				allSucceeded = false
				break
			}
		}
		if allSucceeded && len(scheduledDeps) > 0 {
			helmCmd, err = s.enqueueHelm(ctx, tx, dep, env)
			return err
		}
		return nil
	})

	return scheduledDeps, helmCmd, err
}

// publishScheduledDepEvents publishes CommandQueued events for pending managed deps.
func (s *deploymentService) publishScheduledDepEvents(ctx context.Context, deps []domain.DependencyDeployment, clusterID, deploymentID uuid.UUID) {
	for _, cfg := range deps {
		if cfg.Status == domain.DependencyDeploymentStatusPending && cfg.CommandID != nil {
			s.bus.Publish(ctx, events.ClusterCommandQueuedTopic(clusterID), events.CommandQueued{
				ClusterID:    clusterID,
				CommandID:    *cfg.CommandID,
				DeploymentID: deploymentID,
			})
		}
	}
}
