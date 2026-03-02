package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewPsqlCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "psql",
		Short: "Open a psql session against a PostgreSQL dependency",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: Connect to PostgreSQL dependency via port-forward.
			fmt.Println("psql command: not yet implemented")
			return nil
		},
	}

	return cmd
}
