package app

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	pondconfig "github.com/pondplatform/pond/cli/internal/config"
)

// maybeStartPortForward starts a background kubectl port-forward when the active
// context configures one and its local port is not yet reachable.
func maybeStartPortForward(pf pondconfig.PortForward) error {
	// Gate on the required fields only, so a stray kube-service alone stays silent.
	if pf.KubeContext == "" && pf.KubeNamespace == "" && pf.Port == 0 {
		return nil
	}
	if !pf.Configured() {
		return fmt.Errorf("ignoring incomplete port-forward config: kube-context, kube-namespace and the forward port must all be set")
	}

	addr := net.JoinHostPort("localhost", strconv.Itoa(pf.Port))

	// Skip if already reachable.
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil
	}

	service := pf.KubeService
	if service == "" {
		service = "pond-server"
	}

	cmd := exec.Command("kubectl", "port-forward",
		"--context", pf.KubeContext,
		"--namespace", pf.KubeNamespace,
		fmt.Sprintf("svc/%s", service),
		fmt.Sprintf("%d:8080", pf.Port),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start kubectl port-forward: %w", err)
	}

	// Wait up to 3s for the port to become connectable.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("port-forward to %s/%s did not become ready within 3s", pf.KubeNamespace, service)
}
