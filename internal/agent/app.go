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

func Run(ctx context.Context, cfg Config) error {
	conn := NewConnection(cfg.ServerAddr, cfg.AgentToken)
	if err := conn.Connect(ctx); err != nil {
		return fmt.Errorf("connect to server: %w", err)
	}
	defer conn.Close()

	// The executor is injected with the actual helm/tofu runners in cmd/pond-agent/main.go.
	// This is a placeholder — the real composition happens at the entrypoint.
	log.Println("agent connected, waiting for commands...")

	for {
		cmd, err := conn.ReceiveCommand(ctx)
		if err != nil {
			return fmt.Errorf("receive command: %w", err)
		}

		log.Printf("received command: %s (id=%s)", cmd.Type, cmd.ID)
	}
}
