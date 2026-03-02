package commands

import (
	"fmt"

	"github.com/pondplatform/pond/internal/cli/client"
	"github.com/spf13/cobra"
)

func NewConfigureCmd(serverClient client.ServerClient) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Set dependency configuration for an environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: Interactive prompts for dependency configuration.
			fmt.Println("configure command: interactive setup not yet implemented")
			return nil
		},
	}

	return cmd
}
