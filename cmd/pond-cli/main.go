package main

import (
	"fmt"
	"os"

	"github.com/pondplatform/pond/internal/cli"
)

func main() {
	serverURL := os.Getenv("POND_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}

	root := cli.NewRootCmd(serverURL)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
