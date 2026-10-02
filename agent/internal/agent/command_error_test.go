package agent_test

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/pondplatform/pond/agent/internal/agent"
)

func TestRunLogged_FailureQuotesOutputAndExitCode(t *testing.T) {
	var logs bytes.Buffer
	cmd := exec.Command("sh", "-c", `echo "starting"; echo "Error: UPGRADE FAILED: boom" >&2; exit 3`)

	err := agent.RunLogged(context.Background(), cmd, &logs, `helm upgrade of release "api"`)
	if err == nil {
		t.Fatal("expected error")
	}
	want := "helm upgrade of release \"api\" failed (exit code 3):\nstarting\nError: UPGRADE FAILED: boom"
	if err.Error() != want {
		t.Errorf("error =\n%s\nwant\n%s", err, want)
	}
	if !strings.Contains(logs.String(), "starting") || !strings.Contains(logs.String(), "UPGRADE FAILED") {
		t.Errorf("expected both streams in the log, got %q", logs.String())
	}
}

func TestRunLogged_SuccessReturnsNil(t *testing.T) {
	var logs bytes.Buffer
	if err := agent.RunLogged(context.Background(), exec.Command("sh", "-c", "echo ok"), &logs, "step"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logs.String() != "ok\n" {
		t.Errorf("log = %q", logs.String())
	}
}

func TestRunLogged_KeepsOnlyTheTail(t *testing.T) {
	cmd := exec.Command("sh", "-c", `i=1; while [ $i -le 100 ]; do echo "line $i"; i=$((i+1)); done; exit 1`)

	err := agent.RunLogged(context.Background(), cmd, &bytes.Buffer{}, "step")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "line 100") {
		t.Errorf("expected last line in error, got:\n%s", msg)
	}
	if strings.Contains(msg, "line 1\n") {
		t.Errorf("expected early lines to be dropped, got:\n%s", msg)
	}
	if !strings.Contains(msg, "\n...\n") {
		t.Errorf("expected truncation marker, got:\n%s", msg)
	}
}

func TestRunLogged_LongOutputIsBounded(t *testing.T) {
	cmd := exec.Command("sh", "-c", `head -c 100000 /dev/zero | tr '\0' 'x'; echo; echo "real error"; exit 1`)

	err := agent.RunLogged(context.Background(), cmd, &bytes.Buffer{}, "step")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(err.Error()) > 5000 {
		t.Errorf("error is %d bytes; expected the tail to be bounded", len(err.Error()))
	}
	if !strings.HasSuffix(err.Error(), "real error") {
		t.Errorf("expected error to end with the last line, got suffix %q", err.Error()[len(err.Error())-40:])
	}
}

func TestRunLogged_AddsHintForLockedRelease(t *testing.T) {
	cmd := exec.Command("sh", "-c", `echo "Error: UPGRADE FAILED: another operation (install/upgrade/rollback) is in progress" >&2; exit 1`)

	err := agent.RunLogged(context.Background(), cmd, &bytes.Buffer{}, "helm upgrade")
	if err == nil || !strings.Contains(err.Error(), "\nHint: a previous helm operation") {
		t.Fatalf("expected locked-release hint, got: %v", err)
	}
}

func TestRunLogged_MissingBinary(t *testing.T) {
	err := agent.RunLogged(context.Background(), exec.Command("pond-no-such-binary"), &bytes.Buffer{}, "tofu init")
	if err == nil || !strings.HasPrefix(err.Error(), "tofu init failed (") || !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCommandFailed_CancelledContextSaysShuttingDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := agent.CommandFailed(ctx, "tofu apply", fmt.Errorf("signal: killed"), nil)
	if err.Error() != "tofu apply failed (interrupted because the agent is shutting down)" {
		t.Errorf("unexpected error: %v", err)
	}
}
