package app

import (
	"github.com/pondplatform/pond/cli/internal/client"
	"github.com/pondplatform/pond/cli/internal/commands"
	"github.com/spf13/cobra"
)

func NewRootCmd(serverURL, token string) *cobra.Command {
	var serverClient client.ServerClient
	if token != "" {
		serverClient = client.NewHTTPClientWithToken(serverURL, token)
	} else {
		serverClient = client.NewHTTPClient(serverURL)
	}

	root := &cobra.Command{
		Use:          "pond",
		Short:        "Pond CLI — deploy and manage services",
		SilenceUsage: true,
	}

	root.AddCommand(commands.NewDeployCmd(serverClient))
	root.AddCommand(commands.NewDeploymentCmd(serverClient))

	return root
}
