package commands_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pondplatform/pond/cli/internal/commands"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	"github.com/spf13/cobra"
)

func makeContextCmd(t *testing.T) (*cobra.Command, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".pond", "config")
	cfgFn := func() (string, error) { return path, nil }
	cmd := commands.NewContextCmd(cfgFn)
	return cmd, path
}

func runCmd(t *testing.T, root *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestContextListEmpty(t *testing.T) {
	cmd, _ := makeContextCmd(t)
	out, err := runCmd(t, cmd, "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}

func TestContextListShowsActive(t *testing.T) {
	cmd, path := makeContextCmd(t)
	cfg := &pondconfig.Config{
		CurrentContext: "prod",
		Contexts: map[string]pondconfig.Context{
			"prod":    {Server: "http://prod"},
			"staging": {Server: "http://staging"},
		},
	}
	if err := pondconfig.Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := runCmd(t, cmd, "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "* prod") {
		t.Errorf("expected active context marked with *, got:\n%s", out)
	}
	if !strings.Contains(out, "  staging") {
		t.Errorf("expected staging listed, got:\n%s", out)
	}
}

func TestContextUse(t *testing.T) {
	cmd, path := makeContextCmd(t)
	cfg := &pondconfig.Config{
		Contexts: map[string]pondconfig.Context{
			"staging": {Server: "http://staging"},
		},
	}
	if err := pondconfig.Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, err := runCmd(t, cmd, "use", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := pondconfig.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.CurrentContext != "staging" {
		t.Errorf("current context: got %q, want %q", got.CurrentContext, "staging")
	}
}

func TestContextUseUnknown(t *testing.T) {
	cmd, _ := makeContextCmd(t)
	_, err := runCmd(t, cmd, "use", "nonexistent")
	if err == nil {
		t.Error("expected error for unknown context")
	}
}

func TestContextAdd(t *testing.T) {
	cmd, path := makeContextCmd(t)
	_, err := runCmd(t, cmd, "add", "local", "--server", "http://localhost:8080", "--project", "proj-id", "--env", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, err := pondconfig.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	c := cfg.Contexts["local"]
	if c.Server != "http://localhost:8080" {
		t.Errorf("server: got %q", c.Server)
	}
	if c.Project != "proj-id" {
		t.Errorf("project: got %q", c.Project)
	}
}

func TestContextAddUpdate(t *testing.T) {
	cmd, path := makeContextCmd(t)
	runCmd(t, cmd, "add", "local", "--server", "http://old") //nolint
	out, err := runCmd(t, cmd, "add", "local", "--server", "http://new")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "updated") {
		t.Errorf("expected 'updated' in output, got %q", out)
	}

	cfg, _ := pondconfig.Load(path)
	if cfg.Contexts["local"].Server != "http://new" {
		t.Errorf("server not updated")
	}
}

func TestContextRemove(t *testing.T) {
	cmd, path := makeContextCmd(t)
	cfg := &pondconfig.Config{
		Contexts: map[string]pondconfig.Context{
			"staging": {Server: "http://staging"},
		},
	}
	pondconfig.Save(path, cfg) //nolint

	_, err := runCmd(t, cmd, "remove", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := pondconfig.Load(path)
	if _, ok := got.Contexts["staging"]; ok {
		t.Error("expected staging to be removed")
	}
}

func TestContextRemoveActiveWarns(t *testing.T) {
	cmd, path := makeContextCmd(t)
	cfg := &pondconfig.Config{
		CurrentContext: "staging",
		Contexts: map[string]pondconfig.Context{
			"staging": {Server: "http://staging"},
		},
	}
	pondconfig.Save(path, cfg) //nolint

	out, err := runCmd(t, cmd, "remove", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Warning") {
		t.Errorf("expected warning, got %q", out)
	}

	got, _ := pondconfig.Load(path)
	if got.CurrentContext != "" {
		t.Errorf("expected current context to be cleared, got %q", got.CurrentContext)
	}
}

func TestContextShow(t *testing.T) {
	cmd, path := makeContextCmd(t)
	cfg := &pondconfig.Config{
		CurrentContext: "prod",
		Contexts: map[string]pondconfig.Context{
			"prod": {Server: "http://prod", Token: "secrettoken", Project: "proj-uuid", Env: "production"},
		},
	}
	pondconfig.Save(path, cfg) //nolint

	out, err := runCmd(t, cmd, "show")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "sec***") {
		t.Errorf("expected masked token, got %q", out)
	}
	if strings.Contains(out, "secrettoken") {
		t.Errorf("token should be masked, got %q", out)
	}
}

func TestContextCurrent(t *testing.T) {
	cmd, path := makeContextCmd(t)
	cfg := &pondconfig.Config{
		CurrentContext: "prod",
		Contexts:       map[string]pondconfig.Context{"prod": {}},
	}
	pondconfig.Save(path, cfg) //nolint

	out, err := runCmd(t, cmd, "current")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "prod" {
		t.Errorf("expected 'prod', got %q", out)
	}
}

func TestContextCurrentNoContext(t *testing.T) {
	cmd, _ := makeContextCmd(t)
	_, err := runCmd(t, cmd, "current")
	if err == nil {
		t.Error("expected error when no context set")
	}
}
