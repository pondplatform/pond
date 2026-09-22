package commands

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	"github.com/spf13/cobra"
)

const defaultDeploymentListLimit = 20

func NewDeploymentListCmd() *cobra.Command {
	var (
		service    string
		configPath string
		limit      int
	)

	cmd := &cobra.Command{
		Use:   "list [service]",
		Short: "List the deployments of a service",
		Long: "List the deployment history of a service, newest first.\n\n" +
			"The service can be given as an argument or with --service, and otherwise\n" +
			"defaults to the name in ./pond.yml, so it can be omitted when running from a\n" +
			"service directory.\n\n" +
			"When an environment is set (--env, POND_ENV or the active context), only that\n" +
			"environment's deployments are listed.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 {
				return fmt.Errorf("--limit must be at least 1")
			}
			if service != "" && len(args) > 0 {
				return fmt.Errorf("give the service name either as an argument or with --service, not both")
			}
			sc := cmd.Context().Value(pondconfig.ClientKey{}).(client.ServerClient)

			serviceName := service
			if serviceName == "" {
				var err error
				if serviceName, err = resolveServiceName(args, configPath); err != nil {
					return err
				}
			}
			return runDeploymentList(cmd.Context(), cmd.OutOrStdout(), sc, serviceName, limit)
		},
	}

	cmd.Flags().StringVar(&service, "service", "", "Service name (alternative to passing it as an argument)")
	cmd.Flags().StringVarP(&configPath, "config", "c", "./pond.yml", "Path to pond.yml, read for the service name when no service is given")
	cmd.Flags().IntVar(&limit, "limit", defaultDeploymentListLimit, "Maximum number of deployments to list")

	return cmd
}

func runDeploymentList(ctx context.Context, out io.Writer, c client.ServerClient, serviceName string, limit int) error {
	resolved, projID, err := projectFromContext(ctx)
	if err != nil {
		return err
	}

	svc, err := findService(ctx, c, projID, serviceName)
	if err != nil {
		return err
	}

	envs, err := c.ListEnvironments(ctx, projID)
	if err != nil {
		return fmt.Errorf("list environments: %w", err)
	}
	sort.Slice(envs, func(i, j int) bool { return envs[i].Name < envs[j].Name })

	var envFilter *uuid.UUID
	if resolved.Env != "" {
		env, ok := findEnvironment(envs, resolved.Env)
		if !ok {
			return fmt.Errorf("environment %q not found in project %s%s", resolved.Env, projID, availableEnvs(envs))
		}
		envFilter = &env.ID
	}

	items, err := c.ListDeployments(ctx, svc.ID, envFilter, limit)
	if err != nil {
		return fmt.Errorf("list deployments: %w", err)
	}

	if resolved.Env != "" {
		fmt.Fprintf(out, "Service %s (environment: %s)\n\n", svc.Name, resolved.Env)
	} else {
		fmt.Fprintf(out, "Service %s\n\n", svc.Name)
	}
	if len(items) == 0 {
		fmt.Fprintf(out, "No deployments yet.\n")
		return nil
	}

	envNames := make(map[uuid.UUID]string, len(envs))
	for _, e := range envs {
		envNames[e.ID] = e.Name
	}

	// The environment is already in the header when the listing is scoped to one.
	showEnv := envFilter == nil

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	header := []string{"DEPLOYMENT"}
	if showEnv {
		header = append(header, "ENVIRONMENT")
	}
	header = append(header, "STATUS", "IMAGE", "TRIGGERED", "CREATED", "COMPLETED")
	fmt.Fprintln(tw, strings.Join(header, "\t"))

	for _, d := range items {
		row := []string{d.ID.String()}
		if showEnv {
			name, ok := envNames[d.EnvironmentID]
			if !ok {
				name = d.EnvironmentID.String()
			}
			row = append(row, name)
		}
		completed := "-"
		if d.CompletedAt != nil {
			completed = d.CompletedAt.Format("2006-01-02 15:04:05")
		}
		row = append(row, string(d.Status), d.ImageTag, d.TriggeredBy,
			d.CreatedAt.Format("2006-01-02 15:04:05"), completed)
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	return tw.Flush()
}
