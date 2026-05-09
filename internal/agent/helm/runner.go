package helm

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/pondplatform/pond/internal/agent"
	"github.com/pondplatform/pond/internal/common/wire"
)

type runner struct{}

func NewRunner() agent.HelmRunner {
	return &runner{}
}

func (r *runner) Upgrade(ctx context.Context, req wire.HelmUpgradePayload, logW io.Writer) error {
	vf, err := os.CreateTemp("", "pond-helm-*.yaml")
	if err != nil {
		return fmt.Errorf("create values file: %w", err)
	}
	valuesFile := vf.Name()
	//defer os.Remove(valuesFile)
	if _, err := vf.Write(req.Values); err != nil {
		vf.Close()
		return fmt.Errorf("write values file: %w", err)
	}
	vf.Close()

	args := []string{
		"upgrade", "--install",
		req.ReleaseName,
		req.ChartPath,
		"--namespace", req.Namespace,
		"--values", valuesFile,
		"--wait",
	}

	cmd := exec.CommandContext(ctx, "helm", args...)
	_, _ = fmt.Fprintf(logW, "Executing command %s", cmd.String())
	cmd.Stdout = logW
	cmd.Stderr = logW

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helm upgrade: %w", err)
	}

	return nil
}
