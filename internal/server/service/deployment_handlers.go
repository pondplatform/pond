package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/events"
)

// handleAgentReady dispatches the next queued command for the cluster.
func (s *deploymentService) handleAgentReady(ctx context.Context, e events.AgentReady) {
	cmds, err := s.deploymentInfo.ListQueuedCommandsByCluster(ctx, e.ClusterID)
	if err != nil {
		log.Printf("deployment service: list queued commands for cluster %s: %v", e.ClusterID, err)
		return
	}
	if len(cmds) == 0 {
		return
	}
	cmd := cmds[0]
	cmd.Status = domain.CommandStatusDispatched
	cmd.UpdatedAt = time.Now()
	if err := s.deploymentInfo.UpdateCommand(ctx, cmd); err != nil {
		log.Printf("deployment service: update command %s to dispatched: %v", cmd.ID, err)
		return
	}
	s.bus.Publish(ctx, events.ClusterCommandDispatchTopic(e.ClusterID), events.CommandDispatch{Cmd: cmd})
}

// handleCommandStarted transitions a deployment from pending -> running when
// the agent acknowledges the command it just received.
func (s *deploymentService) handleCommandStarted(ctx context.Context, e events.CommandStarted) {
	dep, err := s.deploymentInfo.GetByID(ctx, e.DeploymentID)
	if err != nil {
		log.Printf("deployment service: get deployment %s for command_started: %v", e.DeploymentID, err)
		return
	}
	if dep.Status != domain.DeploymentStatusPending {
		return
	}
	if err := s.deploymentInfo.UpdateStatus(ctx, e.DeploymentID, domain.DeploymentStatusRunning, nil); err != nil {
		log.Printf("deployment service: mark running %s: %v", e.DeploymentID, err)
	}
}

// handleCommandLog persists a streamed log line from a running command.
func (s *deploymentService) handleCommandLog(ctx context.Context, e events.CommandLog) {
	if err := s.deploymentInfo.AppendLog(ctx, e.CommandID, e.Line); err != nil {
		log.Printf("deployment service: append log for command %s: %v", e.CommandID, err)
	}
}

// handleAgentDisconnected requeues the in-flight command (if any) so it can
// be redelivered to the next connected agent for the same cluster.
func (s *deploymentService) handleAgentDisconnected(ctx context.Context, e events.AgentDisconnected) {
	if e.InFlightCommandID == uuid.Nil {
		return
	}
	cmd, err := s.deploymentInfo.GetCommand(ctx, e.InFlightCommandID)
	if err != nil {
		log.Printf("deployment service: get command %s on disconnect: %v", e.InFlightCommandID, err)
		return
	}
	if cmd.Status != domain.CommandStatusDispatched {
		return // already completed or cancelled
	}
	cmd.Status = domain.CommandStatusQueued
	cmd.UpdatedAt = time.Now()
	if err := s.deploymentInfo.UpdateCommand(ctx, cmd); err != nil {
		log.Printf("deployment service: requeue command %s on disconnect: %v", e.InFlightCommandID, err)
	}
}

// handleUserInputProvided checks if all dependencies now have their input provided.
// Only when ALL dependencies have input will it schedule ALL of them at once.
func (s *deploymentService) handleUserInputProvided(ctx context.Context, e events.UserInputProvided) {
	dep, err := s.deploymentInfo.GetByID(ctx, e.DeploymentID)
	if err != nil {
		log.Printf("deployment service: get deployment %s for user_input.provided: %v", e.DeploymentID, err)
		return
	}
	env, err := s.envs.GetByID(ctx, dep.EnvironmentID)
	if err != nil {
		log.Printf("deployment service: get environment for deployment %s: %v", e.DeploymentID, err)
		return
	}

	// Check if any dependencies still need input
	stillAwaiting, err := s.deploymentInfo.AnyDepConfigAwaitingInput(ctx, e.DeploymentID)
	if err != nil {
		log.Printf("deployment service: check awaiting input for %s: %v", e.DeploymentID, err)
		return
	}

	if stillAwaiting {
		// Not all inputs provided yet - wait for the rest
		return
	}

	// All inputs provided - schedule ALL dependencies at once
	scheduledDeps, helmCmd, err := s.scheduleAllDepsAfterInput(ctx, dep, env)
	if err != nil {
		log.Printf("deployment service: schedule all deps for %s: %v", e.DeploymentID, err)
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
