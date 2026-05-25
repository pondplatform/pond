package service

import (
	"context"
	"time"

	"github.com/pondplatform/pond/server/internal/events"
	domain "github.com/pondplatform/pond/server/internal/model/db"
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
	s.log.Info("command started", "command_id", e.CommandID)
}

// handleCommandLog persists a streamed log line from a running command.
func (s *deploymentService) handleCommandLog(ctx context.Context, e events.CommandLog) {
	if err := s.deploymentInfo.AppendLog(ctx, e.CommandID, e.Line); err != nil {
		s.log.Error("append log", "command_id", e.CommandID, "err", err)
	}
}

// handleAgentDisconnected requeues any dispatched commands for the cluster so
// they can be redelivered to the next connected agent.
func (s *deploymentService) handleAgentDisconnected(ctx context.Context, e events.AgentDisconnected) {
	cmds, err := s.deploymentInfo.ListDispatchedCommandsByCluster(ctx, e.ClusterID)
	if err != nil {
		s.log.Error("list dispatched commands on disconnect", "cluster_id", e.ClusterID, "err", err)
		return
	}
	for _, cmd := range cmds {
		s.log.Info("agent disconnected with in-flight command, requeueing", "command_id", cmd.ID)
		cmd.Status = domain.CommandStatusQueued
		cmd.UpdatedAt = time.Now()
		if err := s.deploymentInfo.UpdateCommand(ctx, cmd); err != nil {
			s.log.Error("requeue command on disconnect", "command_id", cmd.ID, "err", err)
		}
	}
}

// handleUserInputProvided checks if all dependencies now have their input provided.
// Only when ALL dependencies have input will it schedule ALL of them at once.
func (s *deploymentService) handleUserInputProvided(ctx context.Context, e events.UserInputProvided) {
	s.log.Info("user input provided", "deployment_id", e.DeploymentID)
	if err := s.advanceDependencyStatus(ctx, e.DeploymentID); err != nil {
		s.log.Error("Error handling user input provided event", "err", err)
	}
}
