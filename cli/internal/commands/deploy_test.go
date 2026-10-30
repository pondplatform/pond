package commands_test

import (
	"context"
	"maps"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	"github.com/pondplatform/pond/cli/internal/commands"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
)

const deployTestPondYml = `version: 1
name: api
image: ghcr.io/acme/api
dependencies:
  db:
    type: postgres
`

func runDeployCmd(t *testing.T, c client.ServerClient, args ...string) (string, error) {
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

const (
	testDBCommandID    = "77777777-7777-7777-7777-777777777777"
	testCacheCommandID = "88888888-8888-8888-8888-888888888888"
)

var (
	waitStart = time.Date(2026, 9, 12, 10, 30, 0, 0, time.UTC)
	waitDeps  = map[string]api.DependencyDeploymentSummary{
		testDBCommandID:    {Name: "db", Type: "postgres"},
		testCacheCommandID: {Name: "cache", Type: "redis"},
	}
)

func tofuCmd(id string, status api.CommandStatus, took time.Duration) api.CommandSummary {
	return api.CommandSummary{ID: uuid.MustParse(id), Type: "tofu.apply", Status: status, CreatedAt: waitStart, UpdatedAt: waitStart.Add(took)}
}

func helmCmd(status api.CommandStatus, took time.Duration) api.CommandSummary {
	return api.CommandSummary{ID: uuid.MustParse(testCommandID), Type: "helm.upgrade", Status: status, CreatedAt: waitStart, UpdatedAt: waitStart.Add(took)}
}

// waitPoll is a deployment snapshot with a managed dependency for each tofu.apply.
func waitPoll(status api.DeploymentStatus, cmds ...api.CommandSummary) followPoll {
	d := *newFakeClient().detail
	d.Status = status
	d.CreatedAt = waitStart
	d.Commands = cmds
	for _, cmd := range cmds {
		if cmd.Type == "tofu.apply" {
			dep := waitDeps[cmd.ID.String()]
			managed, id := true, cmd.ID
			dep.Managed, dep.CommandID = &managed, &id
			d.Dependencies = append(d.Dependencies, dep)
		}
	}
	if status == api.DeploymentStatusSucceeded {
		completed := waitStart.Add(88 * time.Second)
		d.CompletedAt = &completed
	}
	return followPoll{deployment: &d, logs: map[uuid.UUID][]string{}}
}

func (p followPoll) withLogs(id string, lines ...string) followPoll {
	p.logs[uuid.MustParse(id)] = lines
	return p
}

func runDeployWait(t *testing.T, polls ...followPoll) (string, error) {
	t.Helper()
	commands.SetPollInterval(t, time.Millisecond)
	pondFile := filepath.Join(t.TempDir(), "pond.yml")
	writeFile(t, pondFile, deployTestPondYml)
	c := &followClient{fakeClient: newFakeClient(), polls: polls}
	return runDeployCmd(t, c, "-f", pondFile, "-t", "v1.2.3")
}

func TestDeployNoWaitExitsOnceSubmitted(t *testing.T) {
	pondFile := filepath.Join(t.TempDir(), "pond.yml")
	writeFile(t, pondFile, deployTestPondYml)
	c := newFakeClient()
	c.detail = nil // waiting would fail to get the deployment

	out, err := runDeployCmd(t, c, "-f", pondFile, "-t", "v1", "--no-wait")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "Deployment submitted: " + testDeployID + " (status: pending)\n"; out != want {
		t.Errorf("expected only %q, got:\n%s", want, out)
	}
}

func TestDeployStillAcceptsWait(t *testing.T) {
	commands.SetPollInterval(t, time.Millisecond)
	pondFile := filepath.Join(t.TempDir(), "pond.yml")
	writeFile(t, pondFile, deployTestPondYml)
	c := &followClient{fakeClient: newFakeClient(), polls: []followPoll{waitPoll(api.DeploymentStatusSucceeded)}}

	out, err := runDeployCmd(t, c, "-f", pondFile, "-t", "v1.2.3", "--wait")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertInOrder(t, out, "Flag --wait has been deprecated", "Deploying api", "Deployment succeeded")
}

func assertInOrder(t *testing.T, out string, want ...string) {
	t.Helper()
	pos := 0
	for _, w := range want {
		i := strings.Index(out[pos:], w)
		if i < 0 {
			t.Fatalf("expected %q after position %d, got:\n%s", w, pos, out)
		}
		pos += i + len(w)
	}
}

func TestDeployWaitShowsEachStep(t *testing.T) {
	out, err := runDeployWait(t,
		waitPoll(api.DeploymentStatusPending, tofuCmd(testDBCommandID, api.CommandStatusQueued, 0)),
		waitPoll(api.DeploymentStatusRunning, tofuCmd(testDBCommandID, api.CommandStatusDispatched, 0)),
		waitPoll(api.DeploymentStatusRunning, tofuCmd(testDBCommandID, api.CommandStatusDispatched, 0)),
		waitPoll(api.DeploymentStatusRunning, tofuCmd(testDBCommandID, api.CommandStatusSucceeded, 42*time.Second), helmCmd(api.CommandStatusQueued, 0)),
		waitPoll(api.DeploymentStatusRunning, tofuCmd(testDBCommandID, api.CommandStatusSucceeded, 42*time.Second), helmCmd(api.CommandStatusDispatched, 0)),
		waitPoll(api.DeploymentStatusSucceeded, tofuCmd(testDBCommandID, api.CommandStatusSucceeded, 42*time.Second), helmCmd(api.CommandStatusSucceeded, 35*time.Second)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	assertInOrder(t, out,
		"Deployment submitted: "+testDeployID,
		"Deploying api (ghcr.io/acme/api:v1.2.3) to prod\n",
		"[1/2] Provisioning db (postgres): waiting for the agent\n",
		"[1/2] Provisioning db (postgres)\n",
		"✓ Provisioned db (postgres)  42s\n",
		"[2/2] Rolling out api: waiting for the agent\n",
		"[2/2] Rolling out api\n",
		"✓ Rolled out api  35s\n",
		"Deployment succeeded in 1m28s\n",
	)
	if n := strings.Count(out, "[1/2] Provisioning db (postgres)\n"); n != 1 {
		t.Errorf("expected the step once, not once per poll, got it %d times:\n%s", n, out)
	}
	if n := strings.Count(out, "✓ Provisioned db (postgres)"); n != 1 {
		t.Errorf("expected the finished step once, got it %d times:\n%s", n, out)
	}
	if strings.ContainsAny(out, "\r\x1b") {
		t.Errorf("expected plain output without a terminal, got %q", out)
	}
}

func TestDeployWaitHeartbeatShowsLatestLogLine(t *testing.T) {
	commands.SetHeartbeatInterval(t, time.Nanosecond)
	running := waitPoll(api.DeploymentStatusRunning, tofuCmd(testDBCommandID, api.CommandStatusDispatched, 0)).
		withLogs(testDBCommandID, "Initializing", "aws_db_instance.this: Still creating...\t[10s elapsed]", "")
	out, err := runDeployWait(t,
		running,
		running,
		waitPoll(api.DeploymentStatusSucceeded, tofuCmd(testDBCommandID, api.CommandStatusSucceeded, time.Second), helmCmd(api.CommandStatusSucceeded, time.Second)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	assertInOrder(t, out,
		"[1/2] Provisioning db (postgres)\n",
		"[1/2] Provisioning db (postgres) · ",
		" · aws_db_instance.this: Still creating... [10s elapsed]\n",
	)
}

func TestDeployWaitNamesConcurrentSteps(t *testing.T) {
	out, err := runDeployWait(t,
		waitPoll(api.DeploymentStatusRunning,
			tofuCmd(testCacheCommandID, api.CommandStatusDispatched, 0),
			tofuCmd(testDBCommandID, api.CommandStatusDispatched, 0)),
		waitPoll(api.DeploymentStatusSucceeded,
			tofuCmd(testCacheCommandID, api.CommandStatusSucceeded, time.Second),
			tofuCmd(testDBCommandID, api.CommandStatusSucceeded, time.Second),
			helmCmd(api.CommandStatusSucceeded, time.Second)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	assertInOrder(t, out, "[1/3] Provisioning cache (redis), db (postgres)\n")
}

func TestDeployWaitFailedShowsTheStepAndReason(t *testing.T) {
	failed := waitPoll(api.DeploymentStatusFailed, tofuCmd(testDBCommandID, api.CommandStatusFailed, 12*time.Second)).
		withLogs(testDBCommandID, "Error: quota exceeded")
	failed.deployment.Commands[0].Error = "tofu exited 1"
	reason := `dependency "db" failed to provision: tofu exited 1`
	failed.deployment.Error = &reason

	out, err := runDeployWait(t,
		waitPoll(api.DeploymentStatusRunning, tofuCmd(testDBCommandID, api.CommandStatusDispatched, 0)),
		failed,
	)
	if err == nil {
		t.Fatalf("expected an error for a failed deployment, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("expected the error to carry the failure reason, got: %v", err)
	}
	assertInOrder(t, out,
		"✗ Provisioning db (postgres) failed after 12s\n",
		"Error: tofu exited 1",
		"Error: quota exceeded",
	)
}

func TestDeployWaitCancelled(t *testing.T) {
	out, err := runDeployWait(t,
		waitPoll(api.DeploymentStatusCancelling, tofuCmd(testDBCommandID, api.CommandStatusDispatched, 0)),
		waitPoll(api.DeploymentStatusCancelled, tofuCmd(testDBCommandID, api.CommandStatusCancelled, 3*time.Second)),
	)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected a cancelled error, got %v:\n%s", err, out)
	}
	assertInOrder(t, out,
		"[1/2] Cancelling: waiting for the agent to stop provisioning db (postgres)\n",
		"- Provisioning db (postgres) cancelled\n",
		"Deployment cancelled\n",
	)
}

func TestDeployWaitOnTerminalRedrawsOneLine(t *testing.T) {
	const width = 40
	commands.ForceLiveProgress(t, width)
	out, err := runDeployWait(t,
		waitPoll(api.DeploymentStatusRunning, tofuCmd(testDBCommandID, api.CommandStatusDispatched, 0)).
			withLogs(testDBCommandID, "aws_db_instance.this: Still creating... [10s elapsed]"),
		waitPoll(api.DeploymentStatusSucceeded, tofuCmd(testDBCommandID, api.CommandStatusSucceeded, 42*time.Second), helmCmd(api.CommandStatusSucceeded, 35*time.Second)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%q", err, out)
	}

	assertInOrder(t, out,
		"\r\x1b[2K",
		"[1/2] Provisioning db (postgres)",
		"\r\x1b[2K✓ Provisioned db (postgres)  42s\n",
		"✓ Rolled out api  35s\n",
	)
	if !strings.HasSuffix(out, "\r\x1b[2KDeployment succeeded in 1m28s\n") {
		t.Errorf("expected the live line cleared before the result, got %q", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("expected the long live line cut to the width, got %q", out)
	}
	live := 0
	for _, seg := range strings.FieldsFunc(out, func(r rune) bool { return r == '\r' || r == '\n' }) {
		seg = strings.TrimPrefix(seg, "\x1b[2K")
		if r, _ := utf8.DecodeRuneInString(seg); !strings.ContainsRune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏", r) {
			continue
		}
		live++
		if n := utf8.RuneCountInString(seg); n >= width {
			t.Errorf("expected live lines narrower than %d, got %d: %q", width, n, seg)
		}
	}
	if live == 0 {
		t.Errorf("expected a live spinner line, got %q", out)
	}
}
