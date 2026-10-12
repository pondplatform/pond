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
// a poll interval before asking again.
func serve(ctx context.Context, conn AgentConnection, exec CommandExecutor, poll time.Duration, log *slog.Logger) error {
	for {
		if err := conn.SendReady(ctx); err != nil {
			return fmt.Errorf("send ready: %w", err)
		}
		msg, err := conn.ReceiveMessage(ctx)
		if err != nil {
			return fmt.Errorf("receive message: %w", err)
		}

		switch msg.Type {
		case "command":
			if err := runCommand(ctx, conn, exec, msg.Data, log); err != nil {
				return err
			}

		case "idle":
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(poll):
			}

		default:
			log.Warn("unexpected message type", "type", msg.Type)
		}
	}
}

// runCommand executes one command and reports its result.
func runCommand(ctx context.Context, conn AgentConnection, exec CommandExecutor, data json.RawMessage, log *slog.Logger) error {
	var cmd wire.CommandPayload
	if err := json.Unmarshal(data, &cmd); err != nil {
		log.Error("decode command", "err", err)
		return reportUndecodableCommand(ctx, conn, data, err)
	}
	log.Info("received command", "type", cmd.Type, "command_id", cmd.ID)

	logSink := func(entry wire.LogPayload) {
		if err := conn.SendLog(ctx, entry); err != nil {
			log.Warn("send log failed", "err", err)
		}
	}

	result, err := exec.Execute(ctx, &cmd, logSink)
	if err != nil {
		log.Error("execute command failed", "command_id", cmd.ID, "err", err)
		result = &wire.ResultPayload{
			CommandID:    cmd.ID,
			DeploymentID: cmd.DeploymentID,
			Success:      false,
			Error:        err.Error(),
		}
	} else {
		result.CommandID = cmd.ID
		log.Info("command completed", "command_id", cmd.ID, "success", result.Success)
	}

	if err := conn.SendResult(ctx, result); err != nil {
		return fmt.Errorf("send result: %w", err)
	}
	return nil
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
