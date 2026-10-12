package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
)

// EventRouter subscribes to the agent-facing topics: it dispatches commands to
// agents, persists their logs and records command results.
type EventRouter struct {
	deploymentInfo store.DeploymentInfoStore
	results        commandResultRecorder
	topics         events.Topics
	log            *slog.Logger
}

type commandResultRecorder interface {
	RecordResult(ctx context.Context, result events.CommandResult) error
}

// Start subscribes to every topic and blocks until ctx is cancelled. It returns
// an error if any subscription fails.
func (r *EventRouter) Start(ctx context.Context) error {
	var unsubs []func()
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	for _, subscribe := range []func() (func(), error){
		func() (func(), error) {
			return r.topics.CommandResultsSubscriber().Subscribe(ctx, r.handleCommandResult)
		},
		func() (func(), error) {
			return r.topics.AgentReadySubscriber().Subscribe(ctx, r.handleAgentReady)
		},
		func() (func(), error) {
			return r.topics.CommandStartedSubscriber().Subscribe(ctx, r.handleCommandStarted)
		},
		func() (func(), error) {
			return r.topics.CommandLogsSubscriber().Subscribe(ctx, r.handleCommandLog)
		},
		func() (func(), error) {
			return r.topics.AgentDisconnectedSubscriber().Subscribe(ctx, r.handleAgentDisconnected)
		},
	} {
		unsub, err := subscribe()
		if err != nil {
			return err
		}
		unsubs = append(unsubs, unsub)
	}

	<-ctx.Done()
	return nil
}

// handleAgentReady dispatches the next queued command for the cluster.
func (r *EventRouter) handleAgentReady(ctx context.Context, e events.AgentReady) {
	cmds, err := r.deploymentInfo.ListQueuedCommandsByCluster(ctx, e.ClusterID)
	if err != nil {
		r.log.Error("list queued commands", "cluster_id", e.ClusterID, "err", err)
		return
	}
	if len(cmds) == 0 {
		return
	}
	cmd := cmds[0]
	r.log.Info("dispatching command to agent", "cluster_id", e.ClusterID, "command_id", cmd.ID, "type", cmd.Type)
	cmd.Status = db.CommandStatusDispatched
	cmd.UpdatedAt = time.Now()
	if err := r.deploymentInfo.UpdateCommand(ctx, cmd); err != nil {
		r.log.Error("update command to dispatched", "command_id", cmd.ID, "err", err)
		return
	}
	if err := r.topics.CommandDispatchPublisher(e.ClusterID).Publish(ctx, events.CommandDispatch{Cmd: cmd}); err != nil {
		r.log.Error("dispatch command", "command_id", cmd.ID, "err", err)
		// Revert so the command is picked up on the agent's next ready signal
		// instead of sitting in 'dispatched' until it disconnects.
		cmd.Status = db.CommandStatusQueued
		cmd.UpdatedAt = time.Now()
		if err := r.deploymentInfo.UpdateCommand(ctx, cmd); err != nil {
			r.log.Error("revert command to queued", "command_id", cmd.ID, "err", err)
		}
	}
}

// handleCommandResult records a finished command's result.
func (r *EventRouter) handleCommandResult(ctx context.Context, res events.CommandResult) {
	if err := r.results.RecordResult(ctx, res); err != nil {
		r.log.Error("record command result", "command_id", res.CommandID, "err", err)
	}
}

func (r *EventRouter) handleCommandStarted(ctx context.Context, e events.CommandStarted) {
	r.log.Info("command started", "command_id", e.CommandID)
}

// handleCommandLog persists a streamed log line from a running command.
func (r *EventRouter) handleCommandLog(ctx context.Context, e events.CommandLog) {
	if err := r.deploymentInfo.AppendLog(ctx, e.CommandID, e.Line); err != nil {
		r.log.Error("append log", "command_id", e.CommandID, "err", err)
	}
}

// handleAgentDisconnected requeues any dispatched commands for the cluster so
// they can be redelivered to the next connected agent.
func (r *EventRouter) handleAgentDisconnected(ctx context.Context, e events.AgentDisconnected) {
	cmds, err := r.deploymentInfo.ListDispatchedCommandsByCluster(ctx, e.ClusterID)
	if err != nil {
		r.log.Error("list dispatched commands on disconnect", "cluster_id", e.ClusterID, "err", err)
		return
	}
	for _, cmd := range cmds {
		r.log.Info("agent disconnected with in-flight command, requeueing", "command_id", cmd.ID)
		cmd.Status = db.CommandStatusQueued
		cmd.UpdatedAt = time.Now()
		if err := r.deploymentInfo.UpdateCommand(ctx, cmd); err != nil {
			r.log.Error("requeue command on disconnect", "command_id", cmd.ID, "err", err)
		}
	}
}
