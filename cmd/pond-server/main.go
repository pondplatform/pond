package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/pondplatform/pond/internal/server"
)

func main() {
	level := slog.LevelInfo
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		_ = level.UnmarshalText([]byte(v))
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := server.Config{
		DatabaseURL: envOrDefault("DATABASE_URL", "postgres://localhost:5432/pond?sslmode=disable"),
		ListenAddr:  envOrDefault("LISTEN_ADDR", ":8080"),
		RabbitMQURL: envOrDefault("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		AdminKey:    os.Getenv("POND_ADMIN_KEY"),
		JWTSecret:   os.Getenv("POND_JWT_SECRET"),
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
