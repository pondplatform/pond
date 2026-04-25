package cli

import (
	"github.com/pondplatform/pond/internal/cli/client"
	"github.com/pondplatform/pond/internal/cli/commands"
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
		Use:   "pond",
		Short: "Pond CLI — deploy and manage services",
	}

	root.AddCommand(commands.NewDeployCmd(serverClient))
	root.AddCommand(commands.NewConfigureCmd(serverClient))
	root.AddCommand(commands.NewRunCmd())
	root.AddCommand(commands.NewPsqlCmd())
	root.AddCommand(commands.NewPortForwardCmd())
	root.AddCommand(commands.NewConfigGenerateCmd())

	return root
}
