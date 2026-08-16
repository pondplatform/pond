package app

import (
	"context"
	"fmt"

	"github.com/pondplatform/pond/cli/internal/client"
	"github.com/pondplatform/pond/cli/internal/commands"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	"github.com/spf13/cobra"
)

func NewRootCmd() *cobra.Command {
	var gf struct {
		server  string
		token   string
		project string
		env     string
		context string
	}

	root := &cobra.Command{
		Use:          "pond",
		Short:        "Pond CLI — deploy and manage services",
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVarP(&gf.server, "server", "s", "", "Server URL (overrides context and POND_SERVER_URL)")
	root.PersistentFlags().StringVar(&gf.token, "token", "", "Bearer token (overrides context and POND_TOKEN)")
	root.PersistentFlags().StringVar(&gf.project, "project", "", "Project ID (overrides context and POND_PROJECT)")
	root.PersistentFlags().StringVarP(&gf.env, "env", "e", "", "Target environment (overrides context and POND_ENV)")
	root.PersistentFlags().StringVar(&gf.context, "context", "", "Named context to use instead of the active context")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Context subcommands manage the config file and don't need a server client.
		for c := cmd; c != nil; c = c.Parent() {
			if c.Annotations["skipClientInit"] == "true" {
				return nil
			}
		}

		cfgPath, err := pondconfig.DefaultConfigPath()
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
		}, cfg)
		if err != nil {
			return err
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
	root.AddCommand(commands.NewContextCmd(pondconfig.DefaultConfigPath))

	return root
}
