package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
	"github.com/spf13/cobra"
)

func NewDeploymentCancelCmd() *cobra.Command {
	var (
		deploymentID string
		filePath     string
		yes          bool
	)

	cmd := &cobra.Command{
		Use:   "cancel [service]",
		Short: "Cancel a deployment in progress",
		Long: "Cancel the deployment of a service in progress in an environment, or a delete.\n" +
			"Steps that have not started are cancelled at once. The agent is told to stop\n" +
			"the step it runs, and the deployment shows as cancelling until it has.\n\n" +
			"The service name defaults to the name in ./pond.yml, so it can be omitted when\n" +
			"running from a service directory. It needs an environment (--env, POND_ENV or\n" +
			"the active context). The command shows the deployment in progress there and\n" +
			"asks for confirmation; --yes skips that.\n\n" +
			"With --deployment-id, that deployment is cancelled without asking.\n\n" +
			"The command exits non-zero when nothing is in progress to cancel.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			sc := ctx.Value(pondconfig.ClientKey{}).(client.ServerClient)
			out := cmd.OutOrStdout()

			if deploymentID != "" {
				if len(args) > 0 {
					return fmt.Errorf("give either a service name or --deployment-id, not both")
				}
				id, err := uuid.Parse(deploymentID)
				if err != nil {
					return fmt.Errorf("invalid deployment id: %w", err)
				}
				d, err := inProgressDeployment(ctx, sc, id)
				if err != nil {
					return err
				}
				return cancelDeployment(ctx, out, sc, d, "the service")
			}

			name, err := resolveServiceName(args, filePath)
			if err != nil {
				return err
			}
			resolved, projID, err := projectFromContext(ctx)
			if err != nil {
				return err
			}
			if resolved.Env == "" {
				return errors.New("cancel needs an environment: set one (--env, POND_ENV or a context) or pass --deployment-id")
			}
			svc, err := findService(ctx, sc, projID, name)
			if err != nil {
				return err
			}
			envs, err := sc.ListEnvironments(ctx, projID)
			if err != nil {
				return fmt.Errorf("list environments: %w", err)
			}
			env, ok := findEnvironment(envs, resolved.Env)
			if !ok {
				return fmt.Errorf("environment %q not found in project %s%s", resolved.Env, projID, availableEnvs(envs))
			}

			// A service has at most one deployment in progress per
			// environment, and it is always the latest.
			latest, err := latestDeployment(ctx, sc, svc.ID, env.ID)
			if err != nil {
				return err
			}
			if latest == nil {
				return fmt.Errorf("nothing to cancel: service %s has never been deployed to %s", svc.Name, env.Name)
			}
			d, err := inProgressDeployment(ctx, sc, latest.ID)
			if err != nil {
				return err
			}
			if !yes {
				if err := confirmCancel(cmd.InOrStdin(), out, d, svc.Name, env.Name); err != nil {
					return err
				}
			}
			return cancelDeployment(ctx, out, sc, d, svc.Name)
		},
	}

	cmd.Flags().StringVar(&deploymentID, "deployment-id", "", "Cancel a deployment by ID, without asking for confirmation")
	cmd.Flags().StringVarP(&filePath, "pond-file", "f", "./pond.yml", "Path to pond.yml, read for the service name when no service argument is given")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Cancel without asking for confirmation")

	return cmd
}

// inProgressDeployment gets the deployment, and says there is nothing to
// cancel when it is no longer in progress.
func inProgressDeployment(ctx context.Context, c client.ServerClient, id uuid.UUID) (*api.Deployment, error) {
	d, err := c.GetDeployment(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	if d.Status != api.DeploymentStatusPending && d.Status != api.DeploymentStatusRunning {
		return nil, fmt.Errorf("nothing to cancel: deployment %s is not in progress (status: %s)", d.ID, statusLabel(d.Kind, d.Status))
	}
	return d, nil
}

// confirmCancel shows the deployment the cancel stops and reads a yes back
// from in. Anything else, or nothing, aborts.
func confirmCancel(in io.Reader, out io.Writer, d *api.Deployment, service, env string) error {
	if d.Kind == api.DeploymentKindDelete {
		fmt.Fprintf(out, "This cancels the delete of service %s from %s:\n", service, env)
	} else {
		fmt.Fprintf(out, "This cancels the deployment of service %s to %s:\n", service, env)
	}
	step, _ := newDeploySteps(d, service).current(d)
	fmt.Fprintf(out, "  Deployment: %s\n", d.ID)
	fmt.Fprintf(out, "  Status:     %s\n", statusLabel(d.Kind, d.Status))
	fmt.Fprintf(out, "  Step:       %s\n", step)
	if d.ImageTag != "" {
		fmt.Fprintf(out, "  Image:      %s\n", d.ImageTag)
	}
	fmt.Fprintf(out, "  Triggered:  %s\n", d.TriggeredBy)
	fmt.Fprintf(out, "  Created:    %s\n", d.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprint(out, "Cancel it? [y/N]: ")

	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || answer == "") {
		fmt.Fprintln(out)
		return errors.New("no confirmation given: answer y, or pass --yes")
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	}
	return errors.New("not confirmed: nothing was cancelled")
}

// cancelDeployment cancels the deployment and names the steps the agent still
// has to stop. service is how the steps name the service.
func cancelDeployment(ctx context.Context, out io.Writer, c client.ServerClient, d *api.Deployment, service string) error {
	if err := c.CancelDeployment(ctx, d.ID); err != nil {
		return fmt.Errorf("cancel deployment: %w", err)
	}
	noun, what := "Deployment", "deployment"
	if d.Kind == api.DeploymentKindDelete {
		noun, what = "Delete", "delete"
	}
	fmt.Fprintf(out, "%s %s cancelled\n", noun, d.ID)

	d, err := c.GetDeployment(ctx, d.ID)
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}
	var running []api.CommandSummary
	for _, cmd := range d.Commands {
		if cmd.Status == api.CommandStatusDispatched {
			running = append(running, cmd)
		}
	}
	if len(running) > 0 {
		fmt.Fprintf(out, "The agent is told to stop %s; the %s shows as cancelling until it has.\n",
			lowerFirst(newDeploySteps(d, service).join(running)), what)
	}
	return nil
}
