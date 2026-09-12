package app

import (
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"syscall"
	"time"

	pondconfig "github.com/pondplatform/pond/cli/internal/config"
)

// maybeStartPortForward starts a background kubectl port-forward when the active
// context has kube fields set and the server port is not yet reachable.
func maybeStartPortForward(ctx pondconfig.Context, serverURL string) error {
	if ctx.KubeContext == "" || ctx.KubeNamespace == "" {
		return nil
	}

	port := "8080"
	if u, err := url.Parse(serverURL); err == nil && u.Port() != "" {
		port = u.Port()
	}

	// Skip if already reachable.
	conn, err := net.DialTimeout("tcp", "localhost:"+port, 200*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil
	}

	service := ctx.KubeService
	if service == "" {
		service = "pond-server"
	}

	cmd := exec.Command("kubectl", "port-forward",
		"--context", ctx.KubeContext,
		"--namespace", ctx.KubeNamespace,
		fmt.Sprintf("svc/%s", service),
		fmt.Sprintf("%s:8080", port),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start kubectl port-forward: %w", err)
	}

	// Wait up to 3s for the port to become connectable.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", "localhost:"+port, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("port-forward to %s/%s did not become ready within 3s", ctx.KubeNamespace, service)
}
