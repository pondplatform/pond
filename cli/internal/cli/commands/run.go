package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the service locally with dependencies proxied from an environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: Local service runner with dependency proxy.
			fmt.Println("run command: not yet implemented")
			return nil
		},
	}

	return cmd
}
