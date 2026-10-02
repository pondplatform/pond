package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/agent/wire"
)

type Config struct {
	ServerAddr string
	AgentToken string
}

func Run(ctx context.Context, cfg Config, exec CommandExecutor, log *slog.Logger) error {
	log.Info("connecting to server", "addr", cfg.ServerAddr)

	conn := NewConnection(cfg.ServerAddr, cfg.AgentToken)
	if err := conn.Connect(ctx); err != nil {
		return fmt.Errorf("connect to server: %w", err)
	}
	defer conn.Close()

	log.Info("connected, announcing ready")
	if err := conn.SendReady(ctx); err != nil {
		return fmt.Errorf("send ready: %w", err)
	}

	for {
		msg, err := conn.ReceiveMessage(ctx)
		if err != nil {
			return fmt.Errorf("receive message: %w", err)
		}

		switch msg.Type {
		case "command":
			var cmd wire.CommandPayload
			if err := json.Unmarshal(msg.Data, &cmd); err != nil {
				log.Error("decode command", "err", err)
				if err := reportUndecodableCommand(ctx, conn, msg.Data, err); err != nil {
					return err
				}
				continue
			}
			log.Info("received command", "type", cmd.Type, "command_id", cmd.ID)

			if err := conn.SendAck(ctx, &cmd); err != nil {
				log.Warn("send ack failed", "command_id", cmd.ID, "err", err)
			}

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

		case "idle":
			log.Debug("agent idle, waiting for commands")

		default:
			log.Warn("unexpected message type", "type", msg.Type)
		}
	}
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
