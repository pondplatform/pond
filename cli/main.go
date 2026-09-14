package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/pondplatform/pond/cli/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := app.NewRootCmd().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
