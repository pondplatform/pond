package commands

import (
	"fmt"
	"sort"

	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	"github.com/spf13/cobra"
)

func NewContextCmd(cfgPathFn func() (string, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage named contexts (server, token, project, env)",
		Annotations: map[string]string{
			"skipClientInit": "true",
		},
	}

	cmd.AddCommand(newContextListCmd(cfgPathFn))
	cmd.AddCommand(newContextUseCmd(cfgPathFn))
	cmd.AddCommand(newContextAddCmd(cfgPathFn))
	cmd.AddCommand(newContextRemoveCmd(cfgPathFn))
	cmd.AddCommand(newContextShowCmd(cfgPathFn))
	cmd.AddCommand(newContextCurrentCmd(cfgPathFn))

	return cmd
}

func newContextListCmd(cfgPathFn func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all contexts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg(cfgPathFn)
			if err != nil {
				return err
			}
			names := sortedKeys(cfg.Contexts)
			for _, name := range names {
				prefix := "  "
				if name == cfg.CurrentContext {
					prefix = "* "
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n", prefix, name)
			}
			return nil
		},
	}
}

func newContextUseCmd(cfgPathFn func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Set the active context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, cfg, err := loadCfgWithPath(cfgPathFn)
			if err != nil {
				return err
			}
			name := args[0]
			if _, ok := cfg.Contexts[name]; !ok {
				return fmt.Errorf("context %q not found", name)
			}
			cfg.CurrentContext = name
			if err := pondconfig.Save(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Switched to context %q.\n", name)
			return nil
		},
	}
}

func newContextAddCmd(cfgPathFn func() (string, error)) *cobra.Command {
	var (
		server  string
		token   string
		project string
		env     string
	)

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update a context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, cfg, err := loadCfgWithPath(cfgPathFn)
			if err != nil {
				return err
			}
			name := args[0]
			_, exists := cfg.Contexts[name]
			cfg.Contexts[name] = pondconfig.Context{
				Server:  server,
				Token:   token,
				Project: project,
				Env:     env,
			}
			if err := pondconfig.Save(path, cfg); err != nil {
				return err
			}
			if exists {
				fmt.Fprintf(cmd.OutOrStdout(), "Context %q updated.\n", name)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Context %q added.\n", name)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&server, "server", "", "Server URL")
	cmd.Flags().StringVar(&token, "token", "", "Bearer token")
	cmd.Flags().StringVar(&project, "project", "", "Project ID")
	cmd.Flags().StringVar(&env, "env", "", "Target environment")
	cmd.MarkFlagRequired("server")

	return cmd
}

func newContextRemoveCmd(cfgPathFn func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, cfg, err := loadCfgWithPath(cfgPathFn)
			if err != nil {
				return err
			}
			name := args[0]
			if _, ok := cfg.Contexts[name]; !ok {
				return fmt.Errorf("context %q not found", name)
			}
			delete(cfg.Contexts, name)
			if cfg.CurrentContext == name {
				cfg.CurrentContext = ""
				fmt.Fprintf(cmd.OutOrStdout(), "Warning: removed the active context; no context is now active.\n")
			}
			if err := pondconfig.Save(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Context %q removed.\n", name)
			return nil
		},
	}
}

func newContextShowCmd(cfgPathFn func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show details of the active context",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg(cfgPathFn)
			if err != nil {
				return err
			}
			name := cfg.CurrentContext
			if name == "" {
				return fmt.Errorf("no active context; use 'pond context use <name>' to set one")
			}
			ctx, ok := cfg.Contexts[name]
			if !ok {
				return fmt.Errorf("active context %q not found in config", name)
			}
			token := ctx.Token
			if len(token) > 3 {
				token = token[:3] + "***"
			} else if token != "" {
				token = "***"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Context: %s\n", name)
			fmt.Fprintf(cmd.OutOrStdout(), "Server:  %s\n", ctx.Server)
			fmt.Fprintf(cmd.OutOrStdout(), "Token:   %s\n", token)
			fmt.Fprintf(cmd.OutOrStdout(), "Project: %s\n", ctx.Project)
			fmt.Fprintf(cmd.OutOrStdout(), "Env:     %s\n", ctx.Env)
			return nil
		},
	}
}

func newContextCurrentCmd(cfgPathFn func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Print the name of the active context",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg(cfgPathFn)
			if err != nil {
				return err
			}
			if cfg.CurrentContext == "" {
				return fmt.Errorf("no active context; use 'pond context use <name>' to set one")
			}
			fmt.Fprintln(cmd.OutOrStdout(), cfg.CurrentContext)
			return nil
		},
	}
}

func loadCfg(cfgPathFn func() (string, error)) (*pondconfig.Config, error) {
	path, err := cfgPathFn()
	if err != nil {
		return nil, err
	}
	return pondconfig.Load(path)
}

func loadCfgWithPath(cfgPathFn func() (string, error)) (string, *pondconfig.Config, error) {
	path, err := cfgPathFn()
	if err != nil {
		return "", nil, err
	}
	cfg, err := pondconfig.Load(path)
	return path, cfg, err
}

func sortedKeys(m map[string]pondconfig.Context) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
