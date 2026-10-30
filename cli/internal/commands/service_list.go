package commands

import (
	"context"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/pondplatform/pond/cli/internal/client"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
	"github.com/spf13/cobra"
)

func NewServiceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the services of the project",
		Long: "List the services of the project, with their status: active, deleting or\n" +
			"deleted. Deleted services are listed too.\n\n" +
			"When an environment is set (--env, POND_ENV or the active context), each\n" +
			"service's latest deployment in that environment is shown instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc := cmd.Context().Value(pondconfig.ClientKey{}).(client.ServerClient)
			return runServiceList(cmd.Context(), cmd.OutOrStdout(), sc)
		},
	}
}

func runServiceList(ctx context.Context, out io.Writer, c client.ServerClient) error {
	resolved, projID, err := projectFromContext(ctx)
	if err != nil {
		return err
	}

	services, err := c.ListServices(ctx, projID)
	if err != nil {
		return fmt.Errorf("list services: %w", err)
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })

	if resolved.Env == "" {
		if len(services) == 0 {
			fmt.Fprintf(out, "No services yet.\n")
			return nil
		}
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSTATUS\tCREATED")
		for _, s := range services {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, s.Status, s.CreatedAt.Format("2006-01-02 15:04:05"))
		}
		return tw.Flush()
	}

	envs, err := c.ListEnvironments(ctx, projID)
	if err != nil {
		return fmt.Errorf("list environments: %w", err)
	}
	env, ok := findEnvironment(envs, resolved.Env)
	if !ok {
		return fmt.Errorf("environment %q not found in project %s%s", resolved.Env, projID, availableEnvs(envs))
	}
	return printServicesInEnv(ctx, out, c, services, env)
}

// printServicesInEnv prints one row per service with its latest deployment in
// env, or placeholders when it was never deployed there.
func printServicesInEnv(ctx context.Context, out io.Writer, c client.ServerClient, services []api.Service, env api.Environment) error {
	fmt.Fprintf(out, "Environment %s\n\n", env.Name)
	if len(services) == 0 {
		fmt.Fprintf(out, "No services yet.\n")
		return nil
	}

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tIMAGE\tDEPLOYMENT\tCREATED")
	for _, s := range services {
		latest, err := latestDeployment(ctx, c, s.ID, env.ID)
		if err != nil {
			return err
		}
		if latest == nil {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Name, "-", "-", "-", "-")
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			s.Name, statusLabel(latest.Kind, latest.Status), latest.ImageTag, latest.ID,
			latest.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return tw.Flush()
}
