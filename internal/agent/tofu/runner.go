package tofu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pondplatform/pond/internal/agent"
)

type runner struct{}

func NewRunner() agent.TofuRunner {
	return &runner{}
}

func (r *runner) Init(ctx context.Context, workDir string, logW io.Writer) error {
	cmd := exec.CommandContext(ctx, "tofu", "init")
	cmd.Dir = workDir
	cmd.Stdout = logW
	cmd.Stderr = logW

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tofu init: %w", err)
	}
	return nil
}

func (r *runner) Apply(ctx context.Context, workDir string, statePath string, vars map[string]any, logW io.Writer) error {
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	varFile, err := os.CreateTemp("", "pond-tofu-vars-*.tfvars.json")
	if err != nil {
		return fmt.Errorf("create temp var file: %w", err)
	}
	defer os.Remove(varFile.Name())

	if err := json.NewEncoder(varFile).Encode(vars); err != nil {
		varFile.Close()
		return fmt.Errorf("write vars to temp file: %w", err)
	}
	varFile.Close()

	args := []string{"apply", "-auto-approve", "-input=false", "-state=" + statePath, "-var-file=" + varFile.Name()}

	cmd := exec.CommandContext(ctx, "tofu", args...)
	cmd.Dir = workDir
	cmd.Stdout = logW
	cmd.Stderr = logW

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tofu apply: %w", err)
	}
	return nil
}

func (r *runner) Output(ctx context.Context, workDir string, statePath string) (map[string]any, error) {
	args := []string{"output", "-json"}
	if statePath != "" {
		args = append(args, "-state="+statePath)
	}

	cmd := exec.CommandContext(ctx, "tofu", args...)
	cmd.Dir = workDir

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tofu output: %w", err)
	}

	var tofuOutputs map[string]struct {
		Value interface{} `json:"value"`
	}
	if err := json.Unmarshal(out, &tofuOutputs); err != nil {
		return nil, fmt.Errorf("parse tofu output: %w", err)
	}

	result := make(map[string]any)
	for k, v := range tofuOutputs {
		result[k] = v.Value
	}

	return result, nil
}

func (r *runner) Destroy(ctx context.Context, workDir string, statePath string, vars map[string]any, logW io.Writer) error {
	varFile, err := os.CreateTemp("", "pond-tofu-vars-*.tfvars.json")
	if err != nil {
		return fmt.Errorf("create temp var file: %w", err)
	}
	defer os.Remove(varFile.Name())

	if err := json.NewEncoder(varFile).Encode(vars); err != nil {
		varFile.Close()
		return fmt.Errorf("write vars to temp file: %w", err)
	}
	varFile.Close()

	args := []string{"destroy", "-auto-approve", "-input=false", "-state=" + statePath, "-var-file=" + varFile.Name()}

	cmd := exec.CommandContext(ctx, "tofu", args...)
	cmd.Dir = workDir
	cmd.Stdout = logW
	cmd.Stderr = logW

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tofu destroy: %w", err)
	}
	return nil
}

