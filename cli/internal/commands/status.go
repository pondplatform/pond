package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
	"github.com/spf13/cobra"
)

func NewStatusCmd() *cobra.Command {
	var (
		deploymentID string
		configPath   string
	)

	cmd := &cobra.Command{
		Use:   "status [service]",
		Short: "Show the deployment status of a service",
		Long: "Show the latest deployment of a service in each environment.\n\n" +
			"The service name defaults to the name in ./pond.yml, so it can be omitted when\n" +
			"running from a service directory.\n\n" +
			"When an environment is set (--env, POND_ENV or the active context), only that\n" +
			"environment is shown, with its dependencies, commands and logs.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sc := cmd.Context().Value(pondconfig.ClientKey{}).(client.ServerClient)
			out := cmd.OutOrStdout()

			if deploymentID != "" {
				if len(args) > 0 {
					return fmt.Errorf("give either a service name or --deployment-id, not both")
				}
				id, err := uuid.Parse(deploymentID)
				if err != nil {
					return fmt.Errorf("invalid deployment id: %w", err)
				}
				d, err := sc.GetDeployment(cmd.Context(), id)
				if err != nil {
					return fmt.Errorf("get deployment: %w", err)
				}
				printDeploymentStatus(cmd.Context(), out, sc, d)
				return nil
			}

			serviceName, err := resolveServiceName(args, configPath)
			if err != nil {
				return err
			}
			return runServiceStatus(cmd.Context(), out, sc, serviceName)
		},
	}

	cmd.Flags().StringVar(&deploymentID, "deployment-id", "", "Show a single deployment by ID instead of a service")
	cmd.Flags().StringVarP(&configPath, "config", "c", "./pond.yml", "Path to pond.yml, read for the service name when no service argument is given")

	return cmd
}

// resolveServiceName takes the service name from the positional argument, and
// otherwise from the name declared in the pond.yml at configPath.
func resolveServiceName(args []string, configPath string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}

	cfg, err := config.NewParser().ParseFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("service name is required: pass it as an argument, run from a directory containing %s, or use --deployment-id", configPath)
		}
		return "", fmt.Errorf("parse config: %w", err)
	}
	if cfg.Name == "" {
		return "", fmt.Errorf("%s does not set a service name: pass the service name as an argument", configPath)
	}
	return cfg.Name, nil
}

// projectFromContext returns the resolved CLI config along with its parsed
// project ID.
func projectFromContext(ctx context.Context) (pondconfig.ResolvedConfig, uuid.UUID, error) {
	resolved, ok := ctx.Value(pondconfig.ResolvedConfigKey{}).(pondconfig.ResolvedConfig)
	if !ok {
		return pondconfig.ResolvedConfig{}, uuid.Nil, fmt.Errorf("internal error: resolved config not available")
	}
	if resolved.Project == "" {
		return pondconfig.ResolvedConfig{}, uuid.Nil, fmt.Errorf("project is required: use --project, POND_PROJECT env var, or configure a context")
	}
	projID, err := uuid.Parse(resolved.Project)
	if err != nil {
		return pondconfig.ResolvedConfig{}, uuid.Nil, fmt.Errorf("invalid project id: %w", err)
	}
	return resolved, projID, nil
}

func runServiceStatus(ctx context.Context, out io.Writer, c client.ServerClient, serviceName string) error {
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

	if resolved.Env != "" {
		env, ok := findEnvironment(envs, resolved.Env)
		if !ok {
			return fmt.Errorf("environment %q not found in project %s%s", resolved.Env, projID, availableEnvs(envs))
		}
		return printServiceStatusForEnv(ctx, out, c, svc, env)
	}

	if len(envs) == 0 {
		fmt.Fprintf(out, "No environments found for project %s.\n", projID)
		return nil
	}
	return printServiceStatusAllEnvs(ctx, out, c, svc, envs)
}

// findService resolves a service name to the service in the given project.
func findService(ctx context.Context, c client.ServerClient, projID uuid.UUID, name string) (api.Service, error) {
	services, err := c.ListServices(ctx, projID)
	if err != nil {
		return api.Service{}, fmt.Errorf("list services: %w", err)
	}
	for _, s := range services {
		if s.Name == name {
			return s, nil
		}
	}

	names := make([]string, 0, len(services))
	for _, s := range services {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	available := ""
	if len(names) > 0 {
		available = fmt.Sprintf(" (available: %s)", strings.Join(names, ", "))
	}
	return api.Service{}, fmt.Errorf("service %q not found in project %s%s", name, projID, available)
}

func findEnvironment(envs []api.Environment, name string) (api.Environment, bool) {
	for _, e := range envs {
		if e.Name == name {
			return e, true
		}
	}
	return api.Environment{}, false
}

func availableEnvs(envs []api.Environment) string {
	if len(envs) == 0 {
		return ""
	}
	names := make([]string, 0, len(envs))
	for _, e := range envs {
		names = append(names, e.Name)
	}
	return fmt.Sprintf(" (available: %s)", strings.Join(names, ", "))
}

// printServiceStatusForEnv prints the full status of the service's latest
// deployment in a single environment.
func printServiceStatusForEnv(ctx context.Context, out io.Writer, c client.ServerClient, svc api.Service, env api.Environment) error {
	latest, err := latestDeployment(ctx, c, svc.ID, env.ID)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Service %s (environment: %s)\n\n", svc.Name, env.Name)
	if latest == nil {
		fmt.Fprintf(out, "No deployments yet.\n")
		return nil
	}

	d, err := c.GetDeployment(ctx, latest.ID)
	if err != nil {
		return fmt.Errorf("get deployment %s: %w", latest.ID, err)
	}
	printDeploymentStatus(ctx, out, c, d)
	return nil
}

// printServiceStatusAllEnvs prints one row per environment with the service's
// latest deployment there.
func printServiceStatusAllEnvs(ctx context.Context, out io.Writer, c client.ServerClient, svc api.Service, envs []api.Environment) error {
	fmt.Fprintf(out, "Service %s\n\n", svc.Name)

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ENVIRONMENT\tSTATUS\tIMAGE\tDEPLOYMENT\tCREATED")
	for _, env := range envs {
		latest, err := latestDeployment(ctx, c, svc.ID, env.ID)
		if err != nil {
			return err
		}
		if latest == nil {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", env.Name, "-", "-", "-", "-")
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			env.Name, latest.Status, latest.ImageTag, latest.ID,
			latest.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return tw.Flush()
}

// latestDeployment returns the service's most recent deployment in an
// environment, or nil when it has never been deployed there.
func latestDeployment(ctx context.Context, c client.ServerClient, serviceID, envID uuid.UUID) (*api.DeploymentListItem, error) {
	items, err := c.ListDeployments(ctx, serviceID, &envID, 1)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

func printDeploymentStatus(ctx context.Context, out io.Writer, c client.ServerClient, d *api.Deployment) {
	fmt.Fprintf(out, "Deployment %s\n", d.ID)
	fmt.Fprintf(out, "  Status:     %s\n", d.Status)
	fmt.Fprintf(out, "  Image:      %s\n", d.ImageTag)
	fmt.Fprintf(out, "  Triggered:  %s\n", d.TriggeredBy)
	fmt.Fprintf(out, "  Created:    %s\n", d.CreatedAt.Format("2006-01-02 15:04:05"))
	if d.CompletedAt != nil {
		fmt.Fprintf(out, "  Completed:  %s\n", d.CompletedAt.Format("2006-01-02 15:04:05"))
	}

	if len(d.Dependencies) > 0 {
		fmt.Fprintf(out, "\nDependencies:\n")
		for _, dep := range d.Dependencies {
			managed := "manual"
			if dep.Managed != nil && *dep.Managed {
				managed = "managed"
			}
			fmt.Fprintf(out, "  %-20s  %-15s  [%s]\n", dep.Name, dep.Status, managed)
		}
	}

	if len(d.Commands) > 0 {
		fmt.Fprintf(out, "\nCommands:\n")
		for _, cmd := range d.Commands {
			fmt.Fprintf(out, "  %s  %-15s  %s\n", cmd.ID, cmd.Type, cmd.Status)
			if cmd.Error != "" {
				fmt.Fprintf(out, "    Error: %s\n", cmd.Error)
			}
			printCommandLogsIfNeeded(ctx, out, c, cmd)
		}
	}
}

func printCommandLogsIfNeeded(ctx context.Context, out io.Writer, c client.ServerClient, cmd api.CommandSummary) {
	shouldPrint := cmd.Status == api.CommandStatusFailed ||
		cmd.Status == api.CommandStatusDispatched ||
		cmd.Status == api.CommandStatusQueued

	if !shouldPrint {
		return
	}

	logs, err := c.GetCommandLogs(ctx, cmd.ID)
	if err != nil {
		fmt.Fprintf(out, "    (could not fetch logs: %v)\n", err)
		return
	}

	if len(logs) == 0 {
		return
	}

	fmt.Fprintf(out, "    Logs:\n")
	for _, l := range logs {
		fmt.Fprintf(out, "      %s\n", l.Line)
	}
}
