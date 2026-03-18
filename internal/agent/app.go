package agent

import (
	"context"
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

	log.Println("agent connected, waiting for commands...")

	for {
		cmd, err := conn.ReceiveCommand(ctx)
		if err != nil {
			return fmt.Errorf("receive command: %w", err)
		}

		log.Printf("received command: %s (id=%s)", cmd.Type, cmd.ID)

		logSink := func(entry LogEntry) {
			if err := conn.SendLog(ctx, entry); err != nil {
				log.Printf("send log: %v", err)
			}
		}

		result, err := exec.Execute(ctx, cmd, logSink)
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
	}
}
