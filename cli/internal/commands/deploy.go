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
				input, err := readInput(inputPath)
				if err != nil {
					return err
				}
				req.DeploymentInput = input
			}

			d, err := sc.SubmitDeployment(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("submit deployment: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Deployment submitted: %s (status: %s)\n", d.ID, d.Status)

			if wait {
				image := tag
				if overridable.Image != "" {
					image = overridable.Image + ":" + tag
				}
				fmt.Fprintf(out, "Deploying %s (%s) to %s\n", overridable.Name, image, resolved.Env)
				return waitForDeployment(cmd.Context(), out, sc, d.ID, overridable.Name)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&filePath, "pond-file", "f", "./pond.yml", "Path to pond.yml")
	cmd.Flags().StringVarP(&tag, "tag", "t", "", "Image tag to deploy")
	cmd.Flags().StringVarP(&inputPath, "input", "i", "", "Path to a JSON or YAML file with input for dependencies and variables (default: pond-input-<env>.yml or .json next to the pond file, if present)")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for completion")
	cmd.MarkFlagRequired("tag")

	return cmd
}

// waitForDeployment polls the deployment until it ends, showing each step
// as it finishes and what the deployment is doing in between.
func waitForDeployment(ctx context.Context, out io.Writer, c client.ServerClient, id uuid.UUID, service string) error {
	p := newProgress(out)
	defer p.Stop()

	reported := map[uuid.UUID]bool{}
	for {
		d, err := c.GetDeployment(ctx, id)
		if err != nil {
			return fmt.Errorf("get deployment status: %w", err)
		}

		steps := newDeploySteps(d, service)
		for _, cmd := range d.Commands {
			if commandDone(cmd.Status) && !reported[cmd.ID] {
				reported[cmd.ID] = true
				p.Println(steps.finished(cmd))
			}
		}

		if deploymentDone(d.Status) {
			p.Stop()
			return reportOutcome(ctx, out, c, d)
		}

		text, tailID := steps.current(d)
		p.Update(text, logTail(ctx, c, tailID))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func reportOutcome(ctx context.Context, out io.Writer, c client.ServerClient, d *api.Deployment) error {
	took := ""
	if d.CompletedAt != nil {
		took = " in " + formatDuration(d.CompletedAt.Sub(d.CreatedAt))
	}
	noun, what := "Deployment", "deployment"
	if d.Kind == api.DeploymentKindDelete {
		noun, what = "Delete", "delete"
	}
	switch d.Status {
	case api.DeploymentStatusSucceeded:
		fmt.Fprintf(out, "%s succeeded%s\n", noun, took)
		return nil
	case api.DeploymentStatusFailed:
		fmt.Fprintln(out)
		printDeploymentStatus(ctx, out, c, d)
		if d.Error != nil && *d.Error != "" {
			return fmt.Errorf("%s failed: %s", what, *d.Error)
		}
		return fmt.Errorf("%s failed", what)
	}
	fmt.Fprintf(out, "%s cancelled%s\n", noun, took)
	return fmt.Errorf("%s cancelled", what)
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

func readInput(path string) (api.DeploymentInput, error) {
	var input api.DeploymentInput
	data, err := os.ReadFile(path)
	if err != nil {
		return input, fmt.Errorf("read input %q: %w", path, err)
	}

	if ext := filepath.Ext(path); ext != ".yml" && ext != ".yaml" {
		if err := json.Unmarshal(data, &input); err != nil {
			return input, fmt.Errorf("parse input %q: %w", path, err)
		}
		return input, nil
	}

	// Dependencies go through JSON so the api types' json tags name their fields.
	// Variables don't: yaml.v3 keeps a scalar's text, where JSON would turn 0123 into 83.
	var raw struct {
		Dependencies any               `yaml:"dependencies"`
		Variables    map[string]string `yaml:"variables"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return input, fmt.Errorf("parse input %q: %w", path, err)
	}
	deps, err := json.Marshal(raw.Dependencies)
	if err == nil {
		err = json.Unmarshal(deps, &input.Dependencies)
	}
	if err != nil {
		return input, fmt.Errorf("parse input %q: %w", path, err)
	}
	input.Variables = raw.Variables
	return input, nil
}
