package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
	"github.com/spf13/cobra"
)

func NewConfigureCmd() *cobra.Command {
	var (
		deploymentID string
		inputPath    string
	)

	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Provide dependency input for a deployment awaiting user configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := uuid.Parse(deploymentID)
			if err != nil {
				return fmt.Errorf("invalid deployment id: %w", err)
			}

			sc := cmd.Context().Value(pondconfig.ClientKey{}).(client.ServerClient)

			data, err := os.ReadFile(inputPath)
			if err != nil {
				return fmt.Errorf("read input %q: %w", inputPath, err)
			}

			var req api.ConfigureDeploymentRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return fmt.Errorf("parse input %q: %w", inputPath, err)
			}

			if err := sc.ConfigureDeployment(cmd.Context(), id, req); err != nil {
				return fmt.Errorf("configure deployment: %w", err)
			}

			fmt.Printf("Deployment %s configured successfully\n", id)
			return nil
		},
	}

	cmd.Flags().StringVar(&deploymentID, "deployment-id", "", "Deployment ID to configure")
	cmd.Flags().StringVarP(&inputPath, "input", "i", "", "Path to JSON file with dependency configuration")
	cmd.MarkFlagRequired("deployment-id")
	cmd.MarkFlagRequired("input")

	return cmd
}
