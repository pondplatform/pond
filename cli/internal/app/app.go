package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/pondplatform/pond/cli/internal/client"
	"github.com/pondplatform/pond/cli/internal/commands"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	"github.com/spf13/cobra"
)

func NewRootCmd() *cobra.Command {
	var gf struct {
		server        string
		token         string
		project       string
		env           string
		context       string
		kubeContext   string
		kubeNamespace string
		kubeService   string
		forwardPort   int
	}

	root := &cobra.Command{
		Use:          "pond",
		Short:        "Pond CLI — deploy and manage services",
		SilenceUsage: true,
	}

	overrides := func(envVars ...string) string {
		return fmt.Sprintf("(overrides context and %s)", strings.Join(envVars, " or "))
	}

	root.PersistentFlags().StringVarP(&gf.server, "server", "s", "", "Server URL "+overrides(pondconfig.EnvServerURL))
	root.PersistentFlags().StringVar(&gf.token, "token", "", "Bearer token "+overrides(pondconfig.EnvToken))
	root.PersistentFlags().StringVar(&gf.project, "project", "", "Project ID "+overrides(pondconfig.EnvProject, pondconfig.EnvProjectID))
	root.PersistentFlags().StringVarP(&gf.env, "env", "e", "", "Target environment "+overrides(pondconfig.EnvEnvironment))
	root.PersistentFlags().StringVarP(&gf.context, "context", "c", "", "Named pond context to use instead of the active one (overrides "+pondconfig.EnvContext+")")
	root.PersistentFlags().StringVar(&gf.kubeContext, "kube-context", "", "kubectl context for the port-forward — not the pond context "+overrides(pondconfig.EnvKubeContext))
	root.PersistentFlags().StringVar(&gf.kubeNamespace, "kube-namespace", "", "Kubernetes namespace for the port-forward "+overrides(pondconfig.EnvKubeNamespace))
	root.PersistentFlags().StringVar(&gf.kubeService, "kube-service", "", "Kubernetes service for the port-forward, default pond-server "+overrides(pondconfig.EnvKubeService))
	root.PersistentFlags().IntVar(&gf.forwardPort, "kubectl-forward-port", 0, "Local port the kubectl port-forward binds "+overrides(pondconfig.EnvForwardPort))

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Context subcommands manage the config file and don't need a server client.
		for c := cmd; c != nil; c = c.Parent() {
			if c.Annotations["skipClientInit"] == "true" {
				return nil
			}
		}

		cfgPath, err := pondconfig.ConfigPath()
		if err != nil {
			return err
		}
		cfg, err := pondconfig.Load(cfgPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		resolved, err := pondconfig.Resolve(pondconfig.Flags{
			Server:  gf.server,
			Token:   gf.token,
			Project: gf.project,
			Env:     gf.env,
			Context: gf.context,
			PortForward: pondconfig.PortForward{
				KubeContext:   gf.kubeContext,
				KubeNamespace: gf.kubeNamespace,
				KubeService:   gf.kubeService,
				Port:          gf.forwardPort,
			},
		}, cfg)
		if err != nil {
			return err
		}

		if err := maybeStartPortForward(resolved.PortForward); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
		}

		var sc client.ServerClient
		if resolved.Token != "" {
			sc = client.NewHTTPClientWithToken(resolved.Server, resolved.Token)
		} else {
			sc = client.NewHTTPClient(resolved.Server)
		}

		ctx := context.WithValue(cmd.Context(), pondconfig.ClientKey{}, sc)
		ctx = context.WithValue(ctx, pondconfig.ResolvedConfigKey{}, resolved)
		cmd.SetContext(ctx)
		return nil
	}

	root.AddCommand(commands.NewDeployCmd())
	root.AddCommand(commands.NewDeploymentCmd())
	root.AddCommand(commands.NewContextCmd(pondconfig.ConfigPath))

	return root
}
