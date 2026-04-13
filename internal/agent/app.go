package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

type Config struct {
	ServerAddr string
	AgentToken string
}

func Run(ctx context.Context, cfg Config, exec CommandExecutor) error {
	conn := NewConnection(cfg.ServerAddr, cfg.AgentToken)
	if err := conn.Connect(ctx); err != nil {
		return fmt.Errorf("connect to server: %w", err)
	}
	defer conn.Close()

	log.Println("agent connected, announcing ready...")
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
				log.Printf("decode command: %v", err)
				continue
			}
			log.Printf("received command: %s (id=%s)", cmd.Type, cmd.ID)

			if err := conn.SendAck(ctx, &cmd); err != nil {
				log.Printf("send ack for command %s: %v", cmd.ID, err)
				// Non-fatal: server will stay in 'pending' briefly longer.
			}

			logSink := func(entry LogEntry) {
				if err := conn.SendLog(ctx, entry); err != nil {
					log.Printf("send log: %v", err)
				}
			}

			result, err := exec.Execute(ctx, &cmd, logSink)
			if err != nil {
				log.Printf("execute command %s: %v", cmd.ID, err)
				result = &CommandResult{
					CommandID: cmd.ID,
					Success:   false,
					Error:     err.Error(),
				}
			}

			if err := conn.SendResult(ctx, result); err != nil {
				return fmt.Errorf("send result: %w", err)
			}

		case "idle":
			// Server has no commands for us right now; wait for the next message.
			log.Println("agent idle, waiting for commands...")

		default:
			log.Printf("agent: unexpected message type %q", msg.Type)
		}
	}
}
