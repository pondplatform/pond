package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewConfigGenerateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config-generate",
		Short: "Generate resolved config files for local inspection",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: Config generation.
			fmt.Println("config-generate command: not yet implemented")
			return nil
		},
	}

	return cmd
}
