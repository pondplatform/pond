package main

import (
	"os"

	"github.com/pondplatform/pond/cli/internal/cli"
)

func main() {
	serverURL := os.Getenv("POND_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}

	token := os.Getenv("POND_TOKEN")
	root := cli.NewRootCmd(serverURL, token)
	root.Execute()
}
