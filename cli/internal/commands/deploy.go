package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	api "github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
	"github.com/spf13/cobra"
)

func NewDeployCmd(serverClient client.ServerClient) *cobra.Command {
	var (
		configPath string
		tag        string
		envName    string
		projectID  string
		wait       bool
	)

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Submit a deployment request",
		RunE: func(cmd *cobra.Command, args []string) error {
			parser := config.NewParser()
			overridable, err := parser.ParseFile(configPath)
			if err != nil {
				return fmt.Errorf("parse config: %w", err)
			}

			projID, err := uuid.Parse(projectID)
			if err != nil {
				return fmt.Errorf("invalid project id: %w", err)
			}

			req := api.SubmitRequest{
				ProjectID:         projID,
				EnvironmentName:   envName,
				OverridableConfig: *overridable,
				ImageTag:          tag,
				TriggeredBy:       "cli",
				CreateIfNotExists: true,
			}

			d, err := serverClient.SubmitDeployment(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("submit deployment: %w", err)
			}

			fmt.Printf("Deployment submitted: %s (status: %s)\n", d.ID, d.Status)

			if wait {
				return waitForDeployment(cmd.Context(), serverClient, d.ID)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "./pond.yml", "Path to pond.yml")
	cmd.Flags().StringVarP(&tag, "tag", "t", "", "Image tag to deploy")
	cmd.Flags().StringVarP(&envName, "env", "e", "", "Target environment")
	cmd.Flags().StringVar(&projectID, "project", "", "Project ID")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for completion")
	cmd.MarkFlagRequired("tag")

	return cmd
}

func waitForDeployment(ctx context.Context, c client.ServerClient, id uuid.UUID) error {
	for {
		d, err := c.GetDeployment(ctx, id)
		if err != nil {
			return fmt.Errorf("get deployment status: %w", err)
		}

		switch d.Status {
		case "succeeded":
			fmt.Println("Deployment succeeded!")
			return nil
		case "failed":
			printDeploymentStatus(ctx, c, d)
			return fmt.Errorf("deployment failed")
		case "awaiting_input":
			fmt.Printf("Deployment is awaiting user input. To provide it, run:\n")
			fmt.Printf("  pond deployment configure --deployment-id %s --file <config.json>\n", id)
			return nil
		default:
			fmt.Printf("Status: %s, waiting...\n", d.Status)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
}
