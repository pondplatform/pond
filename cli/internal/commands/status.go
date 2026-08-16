package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	api "github.com/pondplatform/pond/shared/server/api"
	"github.com/spf13/cobra"
)

func NewStatusCmd(serverClient client.ServerClient) *cobra.Command {
	var deploymentID string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the status of a deployment",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := uuid.Parse(deploymentID)
			if err != nil {
				return fmt.Errorf("invalid deployment id: %w", err)
			}

			d, err := serverClient.GetDeployment(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get deployment: %w", err)
			}

			printDeploymentStatus(cmd.Context(), serverClient, d)
			return nil
		},
	}

	cmd.Flags().StringVar(&deploymentID, "deployment-id", "", "Deployment ID")
	cmd.MarkFlagRequired("deployment-id")

	return cmd
}

func printDeploymentStatus(ctx context.Context, c client.ServerClient, d *api.Deployment) {
	fmt.Printf("Deployment %s\n", d.ID)
	fmt.Printf("  Status:     %s\n", d.Status)
	fmt.Printf("  Image:      %s\n", d.ImageTag)
	fmt.Printf("  Triggered:  %s\n", d.TriggeredBy)
	fmt.Printf("  Created:    %s\n", d.CreatedAt.Format("2006-01-02 15:04:05"))
	if d.CompletedAt != nil {
		fmt.Printf("  Completed:  %s\n", d.CompletedAt.Format("2006-01-02 15:04:05"))
	}

	if len(d.Dependencies) > 0 {
		fmt.Printf("\nDependencies:\n")
		for _, dep := range d.Dependencies {
			managed := "manual"
			if dep.Managed != nil && *dep.Managed {
				managed = "managed"
			}
			fmt.Printf("  %-20s  %-15s  [%s]\n", dep.Name, dep.Status, managed)
		}
	}

	if len(d.Commands) > 0 {
		fmt.Printf("\nCommands:\n")
		for _, cmd := range d.Commands {
			fmt.Printf("  %s  %-15s  %s\n", cmd.ID, cmd.Type, cmd.Status)
			if cmd.Error != "" {
				fmt.Printf("    Error: %s\n", cmd.Error)
			}
			printCommandLogsIfNeeded(ctx, c, cmd)
		}
	}
}

func printCommandLogsIfNeeded(ctx context.Context, c client.ServerClient, cmd api.CommandSummary) {
	shouldPrint := cmd.Status == api.CommandStatusFailed ||
		cmd.Status == api.CommandStatusDispatched ||
		cmd.Status == api.CommandStatusQueued

	if !shouldPrint {
		return
	}

	logs, err := c.GetCommandLogs(ctx, cmd.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "    (could not fetch logs: %v)\n", err)
		return
	}

	if len(logs) == 0 {
		return
	}

	fmt.Printf("    Logs:\n")
	for _, l := range logs {
		fmt.Printf("      %s\n", l.Line)
	}
}
