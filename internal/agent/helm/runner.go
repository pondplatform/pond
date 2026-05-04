package helm

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pondplatform/pond/internal/agent"
	"github.com/pondplatform/pond/internal/common/wire"
)

type runner struct{}

func NewRunner() agent.HelmRunner {
	return &runner{}
}

func (r *runner) Upgrade(ctx context.Context, req wire.HelmUpgradePayload, logW io.Writer) error {
	valuesFile := filepath.Join(os.TempDir(), fmt.Sprintf("pond-helm-%s.yaml", req.ReleaseName))
	if err := os.WriteFile(valuesFile, req.Values, 0600); err != nil {
		return fmt.Errorf("write values file: %w", err)
	}
	defer os.Remove(valuesFile)

	args := []string{
		"upgrade", "--install",
		req.ReleaseName,
		req.ChartPath,
		"--namespace", req.Namespace,
		"--values", valuesFile,
		"--wait",
	}

	cmd := exec.CommandContext(ctx, "helm", args...)
	cmd.Stdout = logW
	cmd.Stderr = logW

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helm upgrade: %w", err)
	}

	return nil
}
