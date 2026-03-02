package tofu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/pondplatform/pond/internal/agent"
)

type runner struct{}

func NewRunner() agent.TofuRunner {
	return &runner{}
}

func (r *runner) Init(ctx context.Context, workDir string) error {
	cmd := exec.CommandContext(ctx, "tofu", "init")
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tofu init: %w", err)
	}
	return nil
}

func (r *runner) Apply(ctx context.Context, workDir string, vars map[string]string) error {
	args := []string{"apply", "-auto-approve"}
	for k, v := range vars {
		args = append(args, "-var", fmt.Sprintf("%s=%s", k, v))
	}

	cmd := exec.CommandContext(ctx, "tofu", args...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tofu apply: %w", err)
	}
	return nil
}

func (r *runner) Output(ctx context.Context, workDir string) (map[string]any, error) {
	cmd := exec.CommandContext(ctx, "tofu", "output", "-json")
	cmd.Dir = workDir

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tofu output: %w", err)
	}

	var outputs map[string]any
	if err := json.Unmarshal(out, &outputs); err != nil {
		return nil, fmt.Errorf("parse tofu output: %w", err)
	}
	return outputs, nil
}

func (r *runner) Destroy(ctx context.Context, workDir string, vars map[string]string) error {
	args := []string{"destroy", "-auto-approve"}
	for k, v := range vars {
		args = append(args, "-var", fmt.Sprintf("%s=%s", k, v))
	}

	cmd := exec.CommandContext(ctx, "tofu", args...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tofu destroy: %w", err)
	}
	return nil
}
