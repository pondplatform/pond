package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

const (
	tailMaxBytes = 4096
	tailMaxLines = 20
)

// RunLogged runs cmd with its combined output streamed to logW. On failure the
// returned error names the command (desc) and quotes the last lines it
// printed, since that is where helm and tofu report what went wrong.
func RunLogged(ctx context.Context, cmd *exec.Cmd, logW io.Writer, desc string) error {
	tail := &tailBuffer{}
	// One shared writer keeps stdout and stderr on a single pipe, in order.
	w := io.MultiWriter(logW, tail)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return CommandFailed(ctx, desc, err, tail.buf)
	}
	return nil
}

// CommandFailed builds the user-facing error for a command that did not
// succeed. output is whatever the command printed; only its tail is kept.
func CommandFailed(ctx context.Context, desc string, err error, output []byte) error {
	var reason string
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		reason = "interrupted because the agent is shutting down"
	case errors.As(err, &exitErr) && exitErr.ExitCode() >= 0:
		reason = fmt.Sprintf("exit code %d", exitErr.ExitCode())
	case errors.As(err, &exitErr):
		reason = exitErr.String()
	default:
		reason = err.Error()
	}

	tail := lastLines(output)
	var b strings.Builder
	fmt.Fprintf(&b, "%s failed (%s)", desc, reason)
	if tail != "" {
		b.WriteString(":\n")
		b.WriteString(tail)
	}
	if hint := hintFor(tail); hint != "" {
		b.WriteString("\nHint: ")
		b.WriteString(hint)
	}
	return errors.New(b.String())
}

const podsNotReadyHint = "the release's pods did not become ready in time. Check them with `kubectl get pods -n <namespace>` and `kubectl describe pod`; common causes are image pull errors, crash loops and failing readiness probes."

var knownFailures = []struct {
	match string
	hint  string
}{
	{
		match: "another operation (install/upgrade/rollback) is in progress",
		hint:  "a previous helm operation on this release was interrupted and left it locked. Inspect it with `helm history <release> -n <namespace>` and roll back the pending revision with `helm rollback` before deploying again.",
	},
	{match: "timed out waiting for the condition", hint: podsNotReadyHint},
	{match: "context deadline exceeded", hint: podsNotReadyHint},
}

func hintFor(output string) string {
	for _, f := range knownFailures {
		if strings.Contains(output, f.match) {
			return f.hint
		}
	}
	return ""
}

func lastLines(output []byte) string {
	s := string(output)
	truncated := len(s) > tailMaxBytes
	if truncated {
		s = s[len(s)-tailMaxBytes:]
		// Drop the partial line the byte cut landed in.
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}

	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, " \t\r"); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > tailMaxLines {
		lines = lines[len(lines)-tailMaxLines:]
		truncated = true
	}
	if truncated && len(lines) > 0 {
		lines = append([]string{"..."}, lines...)
	}
	return strings.Join(lines, "\n")
}

// tailBuffer keeps only the last tailMaxBytes written to it.
type tailBuffer struct {
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	// Keep one byte beyond the limit so lastLines can tell the output was cut.
	if over := len(t.buf) - (tailMaxBytes + 1); over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}
