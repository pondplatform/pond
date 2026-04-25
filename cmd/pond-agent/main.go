package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/pondplatform/pond/internal/agent"
	"github.com/pondplatform/pond/internal/agent/helm"
	"github.com/pondplatform/pond/internal/agent/tofu"
)

func main() {
	level := slog.LevelInfo
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		_ = level.UnmarshalText([]byte(v))
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := agent.Config{
		ServerAddr: envOrDefault("POND_SERVER_ADDR", "localhost:8080"),
		AgentToken: os.Getenv("POND_AGENT_TOKEN"),
	}

	exec := agent.NewExecutor(helm.NewRunner(), tofu.NewRunner())

	if err := agent.Run(ctx, cfg, exec, log); err != nil {
		log.Error("agent exited", "err", err)
		os.Exit(1)
	}
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
