package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	server "github.com/pondplatform/pond/server/internal"
	"github.com/pondplatform/pond/server/internal/service"
)

func main() {
	level := slog.LevelInfo
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		_ = level.UnmarshalText([]byte(v))
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	commandTimeout, err := durationEnv("POND_COMMAND_TIMEOUT")
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	cfg := server.Config{
		DatabaseURL:    envOrDefault("DATABASE_URL", "postgres://localhost:5432/pond?sslmode=disable"),
		ListenAddr:     envOrDefault("LISTEN_ADDR", ":8080"),
		ManagementAddr: envOrDefault("MANAGEMENT_ADDR", ":9090"),
		RabbitMQURL:    envOrDefault("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		AdminKey:       os.Getenv("POND_ADMIN_KEY"),
		JWTSecret:      os.Getenv("POND_JWT_SECRET"),
		Sweep:          service.SweepConfig{CommandTimeout: commandTimeout},
	}

	if err := server.Run(ctx, cfg, log); err != nil {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// durationEnv parses key as a Go duration such as "90m"; unset means zero.
func durationEnv(key string) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 90m, got %q", key, v)
	}
	return d, nil
}
