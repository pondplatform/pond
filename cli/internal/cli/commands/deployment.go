package commands

import (
	"github.com/pondplatform/pond/cli/internal/cli/client"
	"github.com/spf13/cobra"
)

func NewDeploymentCmd(serverClient client.ServerClient) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deployment",
		Short: "Manage deployments",
	}

	cmd.AddCommand(NewConfigureCmd(serverClient))

	return cmd
}
