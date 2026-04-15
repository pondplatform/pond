package commands

import (
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/cli/client"
	"github.com/pondplatform/pond/internal/common/config"
	"github.com/pondplatform/pond/internal/server/service"
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

			req := service.SubmitRequest{
				ProjectID:         projID,
				EnvironmentName:   envName,
				OverridableConfig: *overridable,
				ImageTag:          tag,
				TriggeredBy:       "cli",
			}

			d, err := serverClient.SubmitDeployment(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("submit deployment: %w", err)
			}

			fmt.Printf("Deployment submitted: %s (status: %s)\n", d.ID, d.Status)

			if wait {
				return waitForDeployment(cmd, serverClient, d.ID)
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

func waitForDeployment(cmd *cobra.Command, c client.ServerClient, id uuid.UUID) error {
	for {
		d, err := c.GetDeployment(cmd.Context(), id)
		if err != nil {
			return fmt.Errorf("get deployment status: %w", err)
		}

		switch d.Status {
		case "succeeded":
			log.Println("Deployment succeeded!")
			return nil
		case "failed":
			return fmt.Errorf("deployment failed")
		default:
			log.Printf("Status: %s, waiting...", d.Status)
			time.Sleep(2 * time.Second)
		}
	}
}
