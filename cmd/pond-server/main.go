package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/pondplatform/pond/internal/server"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := server.Config{
		DatabaseURL: envOrDefault("DATABASE_URL", "postgres://localhost:5432/pond?sslmode=disable"),
		ListenAddr:  envOrDefault("LISTEN_ADDR", ":8080"),
		AdminKey:    os.Getenv("POND_ADMIN_KEY"),
		JWTSecret:   os.Getenv("POND_JWT_SECRET"),
	}

	if err := server.Run(ctx, cfg); err != nil {
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
