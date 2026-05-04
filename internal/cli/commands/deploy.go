package commands

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/cli/client"
	"github.com/pondplatform/pond/internal/common/config"
	"github.com/pondplatform/pond/internal/common/domain"
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

			req := service.SubmitRequest{
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

func printDeploymentLogs(cmd *cobra.Command, c client.ServerClient, d *domain.Deployment) {
	commandIDs := collectCommandIDs(d)
	for _, id := range commandIDs {
		logs, err := c.GetCommandLogs(cmd.Context(), id)
		if err != nil || len(logs) == 0 {
			continue
		}
		for _, l := range logs {
			fmt.Println(l.Line)
		}
	}
}

func collectCommandIDs(d *domain.Deployment) []uuid.UUID {
	var ids []uuid.UUID
	for _, dep := range d.DependencyConfigs {
		if dep.CommandID != nil {
			ids = append(ids, *dep.CommandID)
		}
	}
	if d.HelmCommandID != nil {
		ids = append(ids, *d.HelmCommandID)
	}
	return ids
}
