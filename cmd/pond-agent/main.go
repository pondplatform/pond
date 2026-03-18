package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/pondplatform/pond/internal/agent"
	"github.com/pondplatform/pond/internal/agent/helm"
	"github.com/pondplatform/pond/internal/agent/tofu"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := agent.Config{
		ServerAddr: envOrDefault("POND_SERVER_ADDR", "localhost:8080"),
		AgentToken: os.Getenv("POND_AGENT_TOKEN"),
	}

	exec := agent.NewExecutor(helm.NewRunner(), tofu.NewRunner())

	if err := agent.Run(ctx, cfg, exec); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
