package helm

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/pondplatform/pond/agent/internal/agent"
	"github.com/pondplatform/pond/shared/agent/wire"
)

type runner struct{}

func NewRunner() agent.HelmRunner {
	return &runner{}
}

func (r *runner) Upgrade(ctx context.Context, req wire.HelmUpgradePayload, logW io.Writer) error {
	vf, err := os.CreateTemp("", "pond-helm-*.yaml")
	if err != nil {
		return fmt.Errorf("create helm values file: %w", err)
	}
	valuesFile := vf.Name()
	if _, err := vf.Write(req.Values); err != nil {
		vf.Close()
		return fmt.Errorf("write helm values file: %w", err)
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

	cmd := agent.Interruptible(exec.CommandContext(ctx, "helm", args...))
	_, _ = fmt.Fprintf(logW, "Executing command %s\n", cmd.String())

	desc := fmt.Sprintf("helm upgrade of release %q in namespace %q", req.ReleaseName, req.Namespace)
	return agent.RunLogged(ctx, cmd, logW, desc)
}

func (r *runner) Uninstall(ctx context.Context, release wire.HelmRelease, logW io.Writer) error {
	// Foreground cascade: --wait then waits for the pods too, so nothing still
	// uses a dependency destroyed next.
	args := []string{
		"uninstall", release.ReleaseName,
		"--namespace", release.Namespace,
		"--ignore-not-found",
		"--cascade", "foreground",
		"--wait",
	}

	cmd := agent.Interruptible(exec.CommandContext(ctx, "helm", args...))
	_, _ = fmt.Fprintf(logW, "Executing command %s\n", cmd.String())

	desc := fmt.Sprintf("helm uninstall of release %q in namespace %q", release.ReleaseName, release.Namespace)
	return agent.RunLogged(ctx, cmd, logW, desc)
}
