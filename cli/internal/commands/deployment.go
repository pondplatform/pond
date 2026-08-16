package commands

import (
	"github.com/pondplatform/pond/cli/internal/client"
	"github.com/spf13/cobra"
)

func NewDeploymentCmd(serverClient client.ServerClient) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deployment",
		Short: "Manage deployments",
	}

	cmd.AddCommand(NewConfigureCmd(serverClient))
	cmd.AddCommand(NewStatusCmd(serverClient))

	return cmd
}
