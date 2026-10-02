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

			deps, err := readDependencyInput(inputPath)
			if err != nil {
				return err
			}

			if err := sc.ConfigureDeployment(cmd.Context(), id, api.ConfigureDeploymentRequest{Dependencies: deps}); err != nil {
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

// readDependencyInput never returns a nil map: on submit, nil means "no input
// supplied", and a file without a dependencies key must not silently mean that.
func readDependencyInput(path string) (map[string]api.DependencyInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read input %q: %w", path, err)
	}

	var req api.ConfigureDeploymentRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("parse input %q: %w", path, err)
	}
	if req.Dependencies == nil {
		req.Dependencies = map[string]api.DependencyInput{}
	}
	return req.Dependencies, nil
}
