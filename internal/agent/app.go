package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
			var cmd Command
			if err := json.Unmarshal(msg.Data, &cmd); err != nil {
				log.Error("decode command", "err", err)
				continue
			}
			log.Info("received command", "type", cmd.Type, "command_id", cmd.ID)

			if err := conn.SendAck(ctx, &cmd); err != nil {
				log.Warn("send ack failed", "command_id", cmd.ID, "err", err)
				// Non-fatal: server will stay in 'pending' briefly longer.
			}

			logSink := func(entry LogEntry) {
				if err := conn.SendLog(ctx, entry); err != nil {
					log.Warn("send log failed", "err", err)
				}
			}

			result, err := exec.Execute(ctx, &cmd, logSink)
			if err != nil {
				log.Error("execute command failed", "command_id", cmd.ID, "err", err)
				result = &CommandResult{
					CommandID: cmd.ID,
					Success:   false,
					Error:     err.Error(),
				}
			} else {
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
