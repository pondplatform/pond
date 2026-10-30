package commands

import (
	"github.com/spf13/cobra"
)

func NewServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage services",
	}

	cmd.AddCommand(NewServiceListCmd())
	cmd.AddCommand(NewServiceDeleteCmd())

	return cmd
}
