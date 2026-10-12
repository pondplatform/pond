package agent

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A command the server cancels gets SIGINT, so it can clean up, and its
// failure says who stopped it.
func TestRunLogged_CancelledByTheServerIsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cmd := Interruptible(exec.CommandContext(ctx, "sh", "-c", `trap 'echo saved state; exit 1' INT; while true; do sleep 0.05; done`))
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel(errCancelledByServer)
	}()

	err := RunLogged(ctx, cmd, io.Discard, "tofu apply")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "tofu apply failed (cancelled by the server)") {
		t.Errorf("expected the server's cancel as the reason, got %q", err)
	}
	if !strings.Contains(err.Error(), "saved state") {
		t.Errorf("expected the command to handle SIGINT before exiting, got %q", err)
	}
}
