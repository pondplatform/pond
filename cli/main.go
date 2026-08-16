package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/pondplatform/pond/cli/internal/app"
)

func main() {
	serverURL := os.Getenv("POND_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}

	token := os.Getenv("POND_TOKEN")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := app.NewRootCmd(serverURL, token)
	root.ExecuteContext(ctx)
}
