package commands_test

import (
	"context"
	"maps"
	"path/filepath"
	"testing"

	"github.com/pondplatform/pond/cli/internal/commands"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
)

const deployTestPondYml = `version: 1
name: api
image: ghcr.io/acme/api
dependencies:
  db:
    type: postgres
`

func runDeployCmd(t *testing.T, c *fakeClient, args ...string) (string, error) {
	t.Helper()
	ctx := context.WithValue(context.Background(), pondconfig.ClientKey{}, c)
	ctx = context.WithValue(ctx, pondconfig.ResolvedConfigKey{}, pondconfig.ResolvedConfig{
		Server:  "http://localhost:8080",
		Project: testProjectID,
		Env:     "prod",
	})

	cmd := commands.NewDeployCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetContext(ctx)
	return runCmd(t, cmd, args...)
}

func TestDeploySendsInputWithSubmit(t *testing.T) {
	dir := t.TempDir()
	pondFile := filepath.Join(dir, "pond.yml")
	inputFile := filepath.Join(dir, "input.json")
	writeFile(t, pondFile, deployTestPondYml)
	writeFile(t, inputFile, `{"dependencies": {"db": {"managed": true, "values": {"instances": 1}}}}`)

	c := newFakeClient()
	if _, err := runDeployCmd(t, c, "-f", pondFile, "-t", "v1", "--input", inputFile); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.submitted) != 1 {
		t.Fatalf("expected one submit, got %d", len(c.submitted))
	}
	db, ok := c.submitted[0].Dependencies["db"]
	if !ok {
		t.Fatalf("expected input for db in the submit request, got %+v", c.submitted[0].Dependencies)
	}
	if !db.Managed || db.Values["instances"] != float64(1) {
		t.Errorf("expected managed db with instances=1, got %+v", db)
	}
}

func TestDeploySendsVariablesFromTheInput(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		// Unquoted YAML scalars keep their text: 0123 is not the octal 83, and 8080 not a float.
		{"input.yml", "dependencies:\n  db:\n    managed: true\nvariables:\n  PIN: 0123\n  PORT: 8080\n  ENABLED: true\n  NAME: Acme\n"},
		{"input.json", `{"dependencies": {"db": {"managed": true}}, "variables": {"PIN": "0123", "PORT": "8080", "ENABLED": "true", "NAME": "Acme"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pondFile := filepath.Join(dir, "pond.yml")
			inputFile := filepath.Join(dir, tc.name)
			writeFile(t, pondFile, deployTestPondYml)
			writeFile(t, inputFile, tc.content)

			c := newFakeClient()
			if _, err := runDeployCmd(t, c, "-f", pondFile, "-t", "v1", "--input", inputFile); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(c.submitted) != 1 {
				t.Fatalf("expected one submit, got %d", len(c.submitted))
			}
			req := c.submitted[0]
			if !req.Dependencies["db"].Managed {
				t.Errorf("expected managed db from the same file, got %+v", req.Dependencies)
			}
			want := map[string]string{"PIN": "0123", "PORT": "8080", "ENABLED": "true", "NAME": "Acme"}
			if !maps.Equal(req.Variables, want) {
				t.Errorf("expected variables %v, got %v", want, req.Variables)
			}
		})
	}
}

func TestDeployRejectsANonScalarVariable(t *testing.T) {
	dir := t.TempDir()
	pondFile := filepath.Join(dir, "pond.yml")
	inputFile := filepath.Join(dir, "input.yml")
	writeFile(t, pondFile, deployTestPondYml)
	writeFile(t, inputFile, "variables:\n  HOSTS: [a, b]\n")

	c := newFakeClient()
	if _, err := runDeployCmd(t, c, "-f", pondFile, "-t", "v1", "--input", inputFile); err == nil {
		t.Fatal("expected an error for a list as a variable's value")
	}
	if len(c.submitted) != 0 {
		t.Errorf("expected no submit, got %d", len(c.submitted))
	}
}

func TestDeployWithoutInputSendsNoDependencies(t *testing.T) {
	dir := t.TempDir()
	pondFile := filepath.Join(dir, "pond.yml")
	writeFile(t, pondFile, deployTestPondYml)

	c := newFakeClient()
	if _, err := runDeployCmd(t, c, "-f", pondFile, "-t", "v1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.submitted) != 1 {
		t.Fatalf("expected one submit, got %d", len(c.submitted))
	}
	if c.submitted[0].Dependencies != nil {
		t.Errorf("expected no dependencies without an input file, got %+v", c.submitted[0].Dependencies)
	}
}

func TestDeployDefaultsToEnvInputFile(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"pond-input-prod.yml", "dependencies:\n  db:\n    managed: true\n    values:\n      instances: 1\n"},
		{"pond-input-prod.json", `{"dependencies": {"db": {"managed": true, "values": {"instances": 1}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pondFile := filepath.Join(dir, "pond.yml")
			writeFile(t, pondFile, deployTestPondYml)
			writeFile(t, filepath.Join(dir, tc.name), tc.content)
			writeFile(t, filepath.Join(dir, "pond-input-dev.yml"), "dependencies:\n  db:\n    managed: false\n")

			c := newFakeClient()
			if _, err := runDeployCmd(t, c, "-f", pondFile, "-t", "v1"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(c.submitted) != 1 {
				t.Fatalf("expected one submit, got %d", len(c.submitted))
			}
			db := c.submitted[0].Dependencies["db"]
			if !db.Managed || db.Values["instances"] != float64(1) {
				t.Errorf("expected managed db with instances=1 from %s, got %+v", tc.name, c.submitted[0].Dependencies)
			}
		})
	}
}

func TestDeployExplicitInputOverridesEnvInputFile(t *testing.T) {
	dir := t.TempDir()
	pondFile := filepath.Join(dir, "pond.yml")
	inputFile := filepath.Join(dir, "input.json")
	writeFile(t, pondFile, deployTestPondYml)
	writeFile(t, inputFile, `{"dependencies": {"db": {"managed": true}}}`)
	writeFile(t, filepath.Join(dir, "pond-input-prod.yml"), "dependencies:\n  db:\n    managed: false\n")

	c := newFakeClient()
	if _, err := runDeployCmd(t, c, "-f", pondFile, "-t", "v1", "--input", inputFile); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.submitted) != 1 || !c.submitted[0].Dependencies["db"].Managed {
		t.Fatalf("expected --input to win over pond-input-prod.yml, got %+v", c.submitted)
	}
}

func TestDeployAmbiguousEnvInputFilesFailBeforeSubmit(t *testing.T) {
	dir := t.TempDir()
	pondFile := filepath.Join(dir, "pond.yml")
	writeFile(t, pondFile, deployTestPondYml)
	writeFile(t, filepath.Join(dir, "pond-input-prod.yml"), "dependencies: {}\n")
	writeFile(t, filepath.Join(dir, "pond-input-prod.json"), `{"dependencies": {}}`)

	c := newFakeClient()
	if _, err := runDeployCmd(t, c, "-f", pondFile, "-t", "v1"); err == nil {
		t.Fatal("expected an error when both pond-input-prod.yml and pond-input-prod.json exist")
	}
	if len(c.submitted) != 0 {
		t.Errorf("expected no submit, got %d", len(c.submitted))
	}
}

func TestDeployMalformedInputFailsBeforeSubmit(t *testing.T) {
	dir := t.TempDir()
	pondFile := filepath.Join(dir, "pond.yml")
	inputFile := filepath.Join(dir, "input.json")
	writeFile(t, pondFile, deployTestPondYml)
	writeFile(t, inputFile, `{"dependencies": `)

	c := newFakeClient()
	if _, err := runDeployCmd(t, c, "-f", pondFile, "-t", "v1", "--input", inputFile); err == nil {
		t.Fatal("expected an error for malformed input")
	}
	if len(c.submitted) != 0 {
		t.Errorf("expected no submit when the input file is malformed, got %d", len(c.submitted))
	}
}
