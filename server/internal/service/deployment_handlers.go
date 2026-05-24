package service

import (
	"context"
	"time"

	"github.com/google/uuid"
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
	s.log.Info("get deployment for command_started", "deployment_id", e.DeploymentID)
	return
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
	if err := s.advanceDependencyStatus(ctx, e.DeploymentID); err != nil {
		s.log.Error("Error handling user input provided event", "err", err)
	}
}
