package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pondplatform/pond/cli/internal/commands"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
)

func runInitCmd(t *testing.T, env string, args ...string) (string, error) {
	t.Helper()
	ctx := context.WithValue(context.Background(), pondconfig.ResolvedConfigKey{}, pondconfig.ResolvedConfig{Env: env})

	cmd := commands.NewInitCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetContext(ctx)
	return runCmd(t, cmd, args...)
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	return path
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		t.Errorf("expected nothing written, found %s", e.Name())
	}
}

// What init writes is only useful if deploy takes it as it is, so the test
// hands both files to deploy rather than checking their text.
func TestInitWritesFilesDeployAccepts(t *testing.T) {
	dir := mkdir(t, filepath.Join(t.TempDir(), "My_App"))
	pondFile := filepath.Join(dir, "pond.yml")

	out, err := runInitCmd(t, "prod", "-f", pondFile, "--image", "ghcr.io/acme/api")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	inputFile := filepath.Join(dir, "pond-input-prod.yml")
	for _, want := range []string{"Created " + pondFile, "Created " + inputFile} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the output, got:\n%s", want, out)
		}
	}

	c := newFakeClient()
	if _, err := runDeployCmd(t, c, "-f", pondFile, "-t", "v1"); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if len(c.submitted) != 1 {
		t.Fatalf("expected one submit, got %d", len(c.submitted))
	}
	sub := c.submitted[0]
	cfg := sub.OverridableConfig.ServiceConfig
	if cfg.Name != "my-app" {
		t.Errorf("expected the name derived from the directory, my-app, got %q", cfg.Name)
	}
	if cfg.Image != "ghcr.io/acme/api" {
		t.Errorf("expected image ghcr.io/acme/api, got %q", cfg.Image)
	}
	if cfg.Service == nil || cfg.Service.Port == nil || *cfg.Service.Port != 8080 {
		t.Errorf("expected service.port 8080, got %+v", cfg.Service)
	}
	if err := config.Validate(&cfg); err != nil {
		t.Errorf("expected the written pond.yml to be valid: %v", err)
	}
	if sub.Dependencies != nil || sub.Variables != nil {
		t.Errorf("expected the commented-out input file to send no input, got %+v", sub.DeploymentInput)
	}
}

func TestInitUsesTheFlags(t *testing.T) {
	dir := t.TempDir()
	pondFile := filepath.Join(dir, "custom.yml")

	if _, err := runInitCmd(t, "prod", "-f", pondFile, "--image", "ghcr.io/acme/api", "--name", "api", "--port", "3000"); err != nil {
		t.Fatalf("init: %v", err)
	}

	cfg, err := config.NewParser().ParseFile(pondFile)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Name != "api" {
		t.Errorf("expected name api, got %q", cfg.Name)
	}
	if cfg.Service == nil || cfg.Service.Port == nil || *cfg.Service.Port != 3000 {
		t.Errorf("expected service.port 3000, got %+v", cfg.Service)
	}
	if _, err := os.Stat(filepath.Join(dir, "pond-input-prod.yml")); err != nil {
		t.Errorf("expected the input file next to the pond file: %v", err)
	}
}

func TestInitRefusesExistingFiles(t *testing.T) {
	for _, existing := range []string{"pond.yml", "pond-input-prod.yml", "pond-input-prod.json"} {
		t.Run(existing, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, existing), "keep me")

			_, err := runInitCmd(t, "prod", "-f", filepath.Join(dir, "pond.yml"), "--image", "ghcr.io/acme/api")
			if err == nil || !strings.Contains(err.Error(), existing+" already exists") {
				t.Fatalf("expected an error naming %s, got %v", existing, err)
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read dir: %v", err)
			}
			if len(entries) != 1 {
				t.Errorf("expected only %s, found %d files", existing, len(entries))
			}
			if data, _ := os.ReadFile(filepath.Join(dir, existing)); string(data) != "keep me" {
				t.Errorf("expected %s untouched, got %q", existing, data)
			}
		})
	}
}

func TestInitWithoutEnvWritesOnlyPondYml(t *testing.T) {
	dir := t.TempDir()

	out, err := runInitCmd(t, "", "-f", filepath.Join(dir, "pond.yml"), "--image", "ghcr.io/acme/api")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "--env") {
		t.Errorf("expected a hint to pass --env, got:\n%s", out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "pond.yml" {
		t.Errorf("expected only pond.yml, got %v", entries)
	}
}

func TestInitRejectsInvalidValues(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"no image":     {args: []string{"--name", "api"}, want: `"image"`},
		"invalid name": {args: []string{"--image", "ghcr.io/acme/api", "--name", "My App"}, want: "config.name"},
		"invalid port": {args: []string{"--image", "ghcr.io/acme/api", "--name", "api", "--port", "0"}, want: "config.service.port"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			args := append([]string{"-f", filepath.Join(dir, "pond.yml")}, tc.args...)

			_, err := runInitCmd(t, "prod", args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error mentioning %s, got %v", tc.want, err)
			}
			assertEmptyDir(t, dir)
		})
	}
}

func TestInitNeedsANameWhenTheDirectoryGivesNone(t *testing.T) {
	dir := mkdir(t, filepath.Join(t.TempDir(), "___"))

	_, err := runInitCmd(t, "prod", "-f", filepath.Join(dir, "pond.yml"), "--image", "ghcr.io/acme/api")
	if err == nil || !strings.Contains(err.Error(), "pass --name") {
		t.Fatalf("expected an error asking for --name, got %v", err)
	}
	assertEmptyDir(t, dir)
}
