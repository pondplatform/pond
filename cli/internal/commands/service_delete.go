package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
	"github.com/spf13/cobra"
)

func NewServiceDeleteCmd() *cobra.Command {
	var (
		filePath string
		keepDeps bool
		yes      bool
		wait     bool
	)

	cmd := &cobra.Command{
		Use:   "delete [service]",
		Short: "Delete a service from every environment",
		Long: "Delete a service from every environment it is deployed to: its helm release is\n" +
			"uninstalled, then its managed dependencies are destroyed, unless --keep-deps is\n" +
			"given. Volumes are kept.\n\n" +
			"The service name defaults to the name in ./pond.yml, so it can be omitted when\n" +
			"running from a service directory.\n\n" +
			"The command asks for the service name to be typed as confirmation; --yes skips\n" +
			"that. It takes no environment: --env and " + pondconfig.EnvEnvironment + " are refused.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A delete that looks scoped to one environment would remove the
			// service from all of them.
			if f := cmd.Flags().Lookup("env"); (f != nil && f.Changed) || os.Getenv(pondconfig.EnvEnvironment) != "" {
				return fmt.Errorf("pond service delete deletes the service from every environment and takes no environment: drop --env and unset %s", pondconfig.EnvEnvironment)
			}
			ctx := cmd.Context()
			sc := ctx.Value(pondconfig.ClientKey{}).(client.ServerClient)
			out := cmd.OutOrStdout()

			resolved, projID, err := projectFromContext(ctx)
			if err != nil {
				return err
			}
			if resolved.Env != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "Ignoring environment %q from the context: the service is deleted from every environment\n", resolved.Env)
			}
			name, err := resolveServiceName(args, filePath)
			if err != nil {
				return err
			}
			svc, err := findService(ctx, sc, projID, name)
			if err != nil {
				return err
			}
			if svc.Status == api.ServiceStatusDeleted {
				fmt.Fprintf(out, "Service %s is already deleted\n", svc.Name)
				return nil
			}

			envs, live, err := liveEnvironments(ctx, sc, projID, svc.ID)
			if err != nil {
				return err
			}
			if len(live) == 0 {
				fmt.Fprintf(out, "Service %s is not deployed to any environment: nothing to delete\n", svc.Name)
				return nil
			}
			if !yes {
				if err := confirmDelete(cmd.InOrStdin(), out, svc.Name, live, keepDeps); err != nil {
					return err
				}
			}

			deployments, err := sc.DeleteService(ctx, svc.ID, api.DeleteServiceRequest{KeepDependencies: keepDeps, TriggeredBy: "cli"})
			if err != nil {
				return fmt.Errorf("delete service: %w", err)
			}
			if len(deployments) == 0 {
				fmt.Fprintf(out, "Service %s is not deployed to any environment: nothing to delete\n", svc.Name)
				return nil
			}
			envNames := make(map[uuid.UUID]string, len(envs))
			for _, env := range envs {
				envNames[env.ID] = env.Name
			}
			for _, d := range deployments {
				fmt.Fprintf(out, "Delete submitted: %s from %s (%s)\n", d.ID, envNames[d.EnvironmentID], d.Status)
			}

			var errs []error
			if wait {
				for _, d := range deployments {
					env := envNames[d.EnvironmentID]
					fmt.Fprintf(out, "Deleting %s from %s\n", svc.Name, env)
					if err := waitForDeployment(ctx, out, sc, d.ID, svc.Name); err != nil {
						if ctx.Err() != nil {
							return err
						}
						errs = append(errs, fmt.Errorf("%s: %w", env, err))
					}
				}
				if len(errs) == 0 {
					fmt.Fprintf(out, "Service %s deleted\n", svc.Name)
				}
			}
			fmt.Fprintf(out, "Volumes are kept: deploying %s again mounts their data again\n", svc.Name)
			return errors.Join(errs...)
		},
	}

	cmd.Flags().StringVarP(&filePath, "pond-file", "f", "./pond.yml", "Path to pond.yml, read for the service name when no service argument is given")
	cmd.Flags().BoolVar(&keepDeps, "keep-deps", false, "Keep the managed dependencies, such as databases: only uninstall the service")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Delete without asking for confirmation")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait until the service is deleted from every environment")

	return cmd
}

// liveEnvironments returns the project's environments, and those the service
// is deployed to: where its latest deployment is not a succeeded delete. Both
// are sorted by name.
func liveEnvironments(ctx context.Context, c client.ServerClient, projID, serviceID uuid.UUID) (all, live []api.Environment, err error) {
	all, err = c.ListEnvironments(ctx, projID)
	if err != nil {
		return nil, nil, fmt.Errorf("list environments: %w", err)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	for _, env := range all {
		latest, err := latestDeployment(ctx, c, serviceID, env.ID)
		if err != nil {
			return nil, nil, err
		}
		if latest != nil && !(latest.Kind == api.DeploymentKindDelete && latest.Status == api.DeploymentStatusSucceeded) {
			live = append(live, env)
		}
	}
	return all, live, nil
}

// confirmDelete says what the delete removes and reads the service name back
// from in. Anything else, or nothing, aborts.
func confirmDelete(in io.Reader, out io.Writer, service string, live []api.Environment, keepDeps bool) error {
	names := make([]string, len(live))
	for i, env := range live {
		names[i] = env.Name
	}
	fmt.Fprintf(out, "This deletes service %s from %s.\n", service, strings.Join(names, ", "))
	if keepDeps {
		fmt.Fprintln(out, "Its managed dependencies are kept.")
	} else {
		fmt.Fprintln(out, "Its managed dependencies, such as databases, are destroyed with it: their data is lost.")
	}
	fmt.Fprintln(out, "Volumes are kept.")
	fmt.Fprint(out, "Type the service name to confirm: ")

	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || answer == "") {
		fmt.Fprintln(out)
		return errors.New("no confirmation given: type the service name, or pass --yes")
	}
	if strings.TrimSpace(answer) != service {
		return fmt.Errorf("%q is not %q: nothing was deleted", strings.TrimSpace(answer), service)
	}
	return nil
}
