package commands

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/cli/client"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
	api "github.com/pondplatform/pond/shared/server/api"
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
		Run: func(cmd *cobra.Command, args []string) {
			parser := config.NewParser()
			overridable, err := parser.ParseFile(configPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: parse config: %v\n", err)
				os.Exit(1)
			}

			projID, err := uuid.Parse(projectID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: invalid project id: %v\n", err)
				os.Exit(1)
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
				fmt.Fprintf(os.Stderr, "Error: submit deployment: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("Deployment submitted: %s (status: %s)\n", d.ID, d.Status)

			if wait {
				waitForDeployment(cmd, serverClient, d.ID)
			}
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

func waitForDeployment(cmd *cobra.Command, c client.ServerClient, id uuid.UUID) {
	for {
		d, err := c.GetDeployment(cmd.Context(), id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: get deployment status: %v\n", err)
			os.Exit(1)
		}

		switch d.Status {
		case "succeeded":
			log.Println("Deployment succeeded!")
			return
		case "failed":
			printDeploymentLogs(cmd, c, d)
			fmt.Fprintln(os.Stderr, "Error: deployment failed")
			os.Exit(1)
		default:
			log.Printf("Status: %s, waiting...", d.Status)
			time.Sleep(2 * time.Second)
		}
	}
}

func printDeploymentLogs(cmd *cobra.Command, c client.ServerClient, d *api.Deployment) {
	_ = d
}
