package tofu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pondplatform/pond/agent/internal/agent"
)

type runner struct{}

func NewRunner() agent.TofuRunner {
	return &runner{}
}

// describe names the step by its provider module (the dependency type), which
// is what the user declared in pond.yml.
func describe(step, workDir string) string {
	return fmt.Sprintf("tofu %s for provider module %q", step, filepath.Base(workDir))
}

func (r *runner) Init(ctx context.Context, workDir string, logW io.Writer) error {
	if _, err := os.Stat(workDir); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("this agent has no provider module %q (expected at %s); the dependency type is not supported by this agent version", filepath.Base(workDir), workDir)
	}

	cmd := agent.Interruptible(exec.CommandContext(ctx, "tofu", "init", "-no-color"))
	cmd.Dir = workDir
	return agent.RunLogged(ctx, cmd, logW, describe("init", workDir))
}

func (r *runner) Apply(ctx context.Context, workDir string, statePath string, vars map[string]any, logW io.Writer) error {
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		return fmt.Errorf("create tofu state directory: %w", err)
	}

	varFile, err := os.CreateTemp("", "pond-tofu-vars-*.tfvars.json")
	if err != nil {
		return fmt.Errorf("create tofu var file: %w", err)
	}
	defer os.Remove(varFile.Name())

	if err := json.NewEncoder(varFile).Encode(vars); err != nil {
		varFile.Close()
		return fmt.Errorf("write tofu var file: %w", err)
	}
	varFile.Close()

	args := []string{"apply", "-auto-approve", "-input=false", "-no-color", "-state=" + statePath, "-var-file=" + varFile.Name()}

	cmd := agent.Interruptible(exec.CommandContext(ctx, "tofu", args...))
	cmd.Dir = workDir
	return agent.RunLogged(ctx, cmd, logW, describe("apply", workDir))
}

func (r *runner) Output(ctx context.Context, workDir string, statePath string) (map[string]any, error) {
	args := []string{"output", "-json", "-no-color"}
	if statePath != "" {
		args = append(args, "-state="+statePath)
	}

	cmd := agent.Interruptible(exec.CommandContext(ctx, "tofu", args...))
	cmd.Dir = workDir

	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = exitErr.Stderr
		}
		return nil, agent.CommandFailed(ctx, describe("output", workDir), err, stderr)
	}

	var tofuOutputs map[string]struct {
		Value interface{} `json:"value"`
	}
	if err := json.Unmarshal(out, &tofuOutputs); err != nil {
		return nil, fmt.Errorf("%s returned output that is not valid JSON (%v); output began with: %q", describe("output", workDir), err, preview(out))
	}

	result := make(map[string]any)
	for k, v := range tofuOutputs {
		result[k] = v.Value
	}

	return result, nil
}

func (r *runner) Destroy(ctx context.Context, workDir string, statePath string, vars map[string]any, logW io.Writer) error {
	// A dependency whose apply never ran has no state yet: destroying it
	// destroys nothing, but tofu still needs the directory to write state to.
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		return fmt.Errorf("create tofu state directory: %w", err)
	}

	varFile, err := os.CreateTemp("", "pond-tofu-vars-*.tfvars.json")
	if err != nil {
		return fmt.Errorf("create tofu var file: %w", err)
	}
	defer os.Remove(varFile.Name())

	if err := json.NewEncoder(varFile).Encode(vars); err != nil {
		varFile.Close()
		return fmt.Errorf("write tofu var file: %w", err)
	}
	varFile.Close()

	args := []string{"destroy", "-auto-approve", "-input=false", "-no-color", "-state=" + statePath, "-var-file=" + varFile.Name()}

	cmd := agent.Interruptible(exec.CommandContext(ctx, "tofu", args...))
	cmd.Dir = workDir
	return agent.RunLogged(ctx, cmd, logW, describe("destroy", workDir))
}

func preview(b []byte) string {
	const max = 200
	if len(b) > max {
		return string(b[:max]) + "..."
	}
	return string(b)
}
