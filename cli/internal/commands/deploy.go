package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
	"github.com/spf13/cobra"
)

func NewDeployCmd() *cobra.Command {
	var (
		configPath string
		tag        string
		wait       bool
	)

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Submit a deployment request",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, ok := cmd.Context().Value(pondconfig.ResolvedConfigKey{}).(pondconfig.ResolvedConfig)
			if !ok {
				return fmt.Errorf("internal error: resolved config not available")
			}
			if resolved.Project == "" {
				return fmt.Errorf("project is required: use --project, POND_PROJECT env var, or configure a context")
			}
			if resolved.Env == "" {
				return fmt.Errorf("env is required: use --env, POND_ENV env var, or configure a context")
			}

			sc := cmd.Context().Value(pondconfig.ClientKey{}).(client.ServerClient)

			parser := config.NewParser()
			overridable, err := parser.ParseFile(configPath)
			if err != nil {
				return fmt.Errorf("parse config: %w", err)
			}

			projID, err := uuid.Parse(resolved.Project)
			if err != nil {
				return fmt.Errorf("invalid project id: %w", err)
			}

			req := api.SubmitRequest{
				ProjectID:         projID,
				EnvironmentName:   resolved.Env,
				OverridableConfig: *overridable,
				ImageTag:          tag,
				TriggeredBy:       "cli",
				CreateIfNotExists: true,
			}

			d, err := sc.SubmitDeployment(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("submit deployment: %w", err)
			}

			fmt.Printf("Deployment submitted: %s (status: %s)\n", d.ID, d.Status)

			if wait {
				return waitForDeployment(cmd.Context(), sc, d.ID)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "./pond.yml", "Path to pond.yml")
	cmd.Flags().StringVarP(&tag, "tag", "t", "", "Image tag to deploy")
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
