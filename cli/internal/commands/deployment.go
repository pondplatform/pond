package commands

import (
	"github.com/spf13/cobra"
)

func NewDeploymentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deployment",
		Short: "Manage deployments",
	}

	cmd.AddCommand(NewConfigureCmd())
	cmd.AddCommand(NewStatusCmd())

	return cmd
}
