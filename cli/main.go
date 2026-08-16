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
	app.NewRootCmd().ExecuteContext(ctx)
}
