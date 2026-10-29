package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/agent/wire"
)

type Config struct {
	ServerAddr string
	AgentToken string
	// PollInterval is how long the agent waits before asking for work again
	// after the server had none.
	PollInterval time.Duration
}

func Run(ctx context.Context, cfg Config, exec CommandExecutor, log *slog.Logger) error {
	log.Info("connecting to server", "addr", cfg.ServerAddr)

	conn := NewConnection(cfg.ServerAddr, cfg.AgentToken)
	if err := conn.Connect(ctx); err != nil {
		return fmt.Errorf("connect to server: %w", err)
	}
	defer conn.Close()

	log.Info("connected")
	return serve(ctx, conn, exec, cfg.PollInterval, log)
}

// serve pulls commands one at a time: it asks for work, runs what it gets,
// reports the result, and asks again. When the server has nothing, it waits
// a poll interval before asking again. While a command runs, it keeps reading,
// so the server can tell it to cancel.
//
// A running command never outlives the connection: when serve returns, it
// cancels the command, and the agent exits. The server relies on that. When
// the cluster's agent connects, the server fails every command it still has
// as dispatched.
func serve(ctx context.Context, conn AgentConnection, exec CommandExecutor, poll time.Duration, log *slog.Logger) error {
	inbox := make(chan *wire.Envelope)
	readErr := make(chan error, 1)
	go func() {
		for {
			msg, err := conn.ReceiveMessage(ctx)
			if err != nil {
				readErr <- err
				return
			}
			select {
			case inbox <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	var running *runningCommand
	defer func() {
		if running != nil {
			running.cancel(nil)
		}
	}()
	var askAgain <-chan time.Time
	if err := conn.SendReady(ctx); err != nil {
		return fmt.Errorf("send ready: %w", err)
	}

	for {
		var finished <-chan *wire.ResultPayload
		if running != nil {
			finished = running.result
		}

		select {
		case <-ctx.Done():
			return nil

		case err := <-readErr:
			return fmt.Errorf("receive message: %w", err)

		case <-askAgain:
			askAgain = nil
			if err := conn.SendReady(ctx); err != nil {
				return fmt.Errorf("send ready: %w", err)
			}

		case result := <-finished:
			running = nil
			if err := conn.SendResult(ctx, result); err != nil {
				return fmt.Errorf("send result: %w", err)
			}
			if err := conn.SendReady(ctx); err != nil {
				return fmt.Errorf("send ready: %w", err)
			}

		case msg := <-inbox:
			switch msg.Type {
			case wire.MessageCommand:
				var cmd wire.CommandPayload
				if err := json.Unmarshal(msg.Data, &cmd); err != nil {
					log.Error("decode command", "err", err)
					if err := reportUndecodableCommand(ctx, conn, msg.Data, err); err != nil {
						return err
					}
					if err := conn.SendReady(ctx); err != nil {
						return fmt.Errorf("send ready: %w", err)
					}
					continue
				}
				running = start(ctx, conn, exec, cmd, log)

			case wire.MessageIdle:
				askAgain = time.After(poll)

			case wire.MessageCancel:
				var cancel wire.CancelPayload
				if err := json.Unmarshal(msg.Data, &cancel); err != nil {
					log.Error("decode cancel", "err", err)
					continue
				}
				if running != nil && running.id == cancel.CommandID {
					log.Info("server cancelled the command", "command_id", cancel.CommandID)
					running.cancel(errCancelledByServer)
				}

			default:
				log.Warn("unexpected message type", "type", msg.Type)
			}
		}
	}
}

// runningCommand is a command executing in the background.
type runningCommand struct {
	id     uuid.UUID
	cancel context.CancelCauseFunc
	result chan *wire.ResultPayload
}

// start runs cmd in the background; its result arrives on result.
func start(ctx context.Context, conn AgentConnection, exec CommandExecutor, cmd wire.CommandPayload, log *slog.Logger) *runningCommand {
	cmdCtx, cancel := context.WithCancelCause(ctx)
	r := &runningCommand{id: cmd.ID, cancel: cancel, result: make(chan *wire.ResultPayload, 1)}
	go func() {
		defer cancel(nil)
		r.result <- execute(cmdCtx, conn, exec, &cmd, log)
	}()
	return r
}

// execute runs one command and returns its result.
func execute(ctx context.Context, conn AgentConnection, exec CommandExecutor, cmd *wire.CommandPayload, log *slog.Logger) *wire.ResultPayload {
	log.Info("received command", "type", cmd.Type, "command_id", cmd.ID)

	logSink := func(entry wire.LogPayload) {
		if err := conn.SendLog(ctx, entry); err != nil {
			log.Warn("send log failed", "err", err)
		}
	}

	result, err := exec.Execute(ctx, cmd, logSink)
	if err != nil {
		log.Error("execute command failed", "command_id", cmd.ID, "err", err)
		return &wire.ResultPayload{
			CommandID:    cmd.ID,
			DeploymentID: cmd.DeploymentID,
			Success:      false,
			Error:        err.Error(),
		}
	}
	result.CommandID = cmd.ID
	log.Info("command completed", "command_id", cmd.ID, "success", result.Success)
	return result
}

// reportUndecodableCommand fails the command on the server so the deployment
// records why, instead of the server waiting for a result that never comes.
// Without a readable ID there is nothing to report against.
func reportUndecodableCommand(ctx context.Context, conn AgentConnection, data json.RawMessage, decodeErr error) error {
	var ref struct {
		ID           uuid.UUID `json:"id"`
		DeploymentID uuid.UUID `json:"deployment_id"`
	}
	if err := json.Unmarshal(data, &ref); err != nil || ref.ID == uuid.Nil {
		return nil
	}
	if err := conn.SendResult(ctx, &wire.ResultPayload{
		CommandID:    ref.ID,
		DeploymentID: ref.DeploymentID,
		Success:      false,
		Error:        fmt.Sprintf("agent could not decode the command (%v); the agent and server versions may differ", decodeErr),
	}); err != nil {
		return fmt.Errorf("send result: %w", err)
	}
	return nil
}
