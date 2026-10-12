package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func NewDeployCmd() *cobra.Command {
	var (
		filePath  string
		tag       string
		inputPath string
		wait      bool
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
				return pondconfig.ErrProjectRequired
			}
			if resolved.Env == "" {
				return fmt.Errorf("env is required: use --env, POND_ENV env var, or configure a context")
			}

			sc := cmd.Context().Value(pondconfig.ClientKey{}).(client.ServerClient)

			parser := config.NewParser()
			overridable, err := parser.ParseFile(filePath)
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

			if inputPath == "" {
				inputPath, err = defaultInputPath(filepath.Dir(filePath), resolved.Env)
				if err != nil {
					return err
				}
				if inputPath != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "Using input from %s\n", inputPath)
				}
			}
			if inputPath != "" {
				deps, err := readDependencyInput(inputPath)
				if err != nil {
					return err
				}
				req.Dependencies = deps
			}

			d, err := sc.SubmitDeployment(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("submit deployment: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Deployment submitted: %s (status: %s)\n", d.ID, d.Status)

			if wait {
				return waitForDeployment(cmd.Context(), cmd.OutOrStdout(), sc, d.ID)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&filePath, "pond-file", "f", "./pond.yml", "Path to pond.yml")
	cmd.Flags().StringVarP(&tag, "tag", "t", "", "Image tag to deploy")
	cmd.Flags().StringVarP(&inputPath, "input", "i", "", "Path to a JSON or YAML file with input for dependencies (default: pond-input-<env>.yml or .json next to the pond file, if present)")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for completion")
	cmd.MarkFlagRequired("tag")

	return cmd
}

func waitForDeployment(ctx context.Context, out io.Writer, c client.ServerClient, id uuid.UUID) error {
	for {
		d, err := c.GetDeployment(ctx, id)
		if err != nil {
			return fmt.Errorf("get deployment status: %w", err)
		}

		switch d.Status {
		case "succeeded":
			fmt.Fprintln(out, "Deployment succeeded!")
			return nil
		case "failed":
			printDeploymentStatus(ctx, out, c, d)
			return fmt.Errorf("deployment failed")
		case "cancelled":
			return fmt.Errorf("deployment cancelled")
		default:
			fmt.Fprintf(out, "Status: %s, waiting...\n", d.Status)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func defaultInputPath(dir, env string) (string, error) {
	var found []string
	for _, ext := range []string{".yml", ".json"} {
		p := filepath.Join(dir, "pond-input-"+env+ext)
		if _, err := os.Stat(p); err == nil {
			found = append(found, p)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("check input %q: %w", p, err)
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("both %s and %s exist: remove one or pass --input", found[0], found[1])
	}
}

func readDependencyInput(path string) (map[string]api.DependencyInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read input %q: %w", path, err)
	}

	// YAML goes through JSON so the api types' json tags stay the one source of field names.
	if ext := filepath.Ext(path); ext == ".yml" || ext == ".yaml" {
		var v any
		if err := yaml.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("parse input %q: %w", path, err)
		}
		if data, err = json.Marshal(v); err != nil {
			return nil, fmt.Errorf("parse input %q: %w", path, err)
		}
	}

	var req api.ConfigureDeploymentRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("parse input %q: %w", path, err)
	}
	return req.Dependencies, nil
}
