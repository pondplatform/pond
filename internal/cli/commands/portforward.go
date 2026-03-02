package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewPortForwardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "port-forward",
		Short: "Forward a port from a running service or dependency",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: Port forwarding.
			fmt.Println("port-forward command: not yet implemented")
			return nil
		},
	}

	return cmd
}
