package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/agent/internal/agent"
	"github.com/pondplatform/pond/shared/agent/wire"
)

// recordingRunners log every runner call, in order, as "<step> <subject>".
func recordingRunners(calls *[]string, failOn string) (*mockHelmRunner, *mockTofuRunner) {
	record := func(call string) error {
		*calls = append(*calls, call)
		if call == failOn {
			return errors.New(call + " failed")
		}
		return nil
	}
	helm := &mockHelmRunner{
		uninstallFn: func(_ context.Context, release wire.HelmRelease, logW io.Writer) error {
			_, _ = io.WriteString(logW, "uninstalling\n")
			return record("uninstall " + release.Namespace + "/" + release.ReleaseName)
		},
	}
	tofu := &mockTofuRunner{
		initFn: func(_ context.Context, workDir string, _ io.Writer) error {
			return record("init " + workDir)
		},
		destroyFn: func(_ context.Context, _ string, statePath string, vars map[string]any, _ io.Writer) error {
			if vars["dependency_name"] == nil {
				return errors.New("destroy got no vars")
			}
			return record("destroy " + statePath)
		},
	}
	return helm, tofu
}

func serviceDeleteCommand(t *testing.T, payload wire.ServiceDeletePayload) *wire.CommandPayload {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return &wire.CommandPayload{ID: uuid.New(), Type: wire.CommandServiceDelete, Payload: raw}
}

func twoDependencies() []wire.TofuDestroyPayload {
	return []wire.TofuDestroyPayload{
		{Name: "cache", WorkDir: "/mods/redis", StatePath: "/states/e/api/cache/terraform.tfstate", Vars: map[string]any{"dependency_name": "cache"}},
		{Name: "db", WorkDir: "/mods/postgres", StatePath: "/states/e/api/db/terraform.tfstate", Vars: map[string]any{"dependency_name": "db"}},
	}
}

func TestExecute_ServiceDelete_UninstallsBeforeDestroying(t *testing.T) {
	var calls []string
	helm, tofu := recordingRunners(&calls, "")
	cmd := serviceDeleteCommand(t, wire.ServiceDeletePayload{
		Release:      &wire.HelmRelease{ReleaseName: "api", Namespace: "prod"},
		Dependencies: twoDependencies(),
	})

	var logLines []string
	result, err := agent.NewExecutor(helm, tofu).Execute(context.Background(), cmd, func(e wire.LogPayload) {
		logLines = append(logLines, e.Line)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	want := []string{
		"uninstall prod/api",
		"init /mods/redis", "destroy /states/e/api/cache/terraform.tfstate",
		"init /mods/postgres", "destroy /states/e/api/db/terraform.tfstate",
	}
	if !slices.Equal(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}
	if len(logLines) == 0 {
		t.Error("expected log lines, got none")
	}
}

func TestExecute_ServiceDelete_WithoutReleaseOnlyDestroys(t *testing.T) {
	var calls []string
	helm, tofu := recordingRunners(&calls, "")
	cmd := serviceDeleteCommand(t, wire.ServiceDeletePayload{Dependencies: twoDependencies()[1:]})

	result, err := agent.NewExecutor(helm, tofu).Execute(context.Background(), cmd, func(wire.LogPayload) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	want := []string{"init /mods/postgres", "destroy /states/e/api/db/terraform.tfstate"}
	if !slices.Equal(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}
}

func TestExecute_ServiceDelete_FailedUninstallDestroysNothing(t *testing.T) {
	var calls []string
	helm, tofu := recordingRunners(&calls, "uninstall prod/api")
	cmd := serviceDeleteCommand(t, wire.ServiceDeletePayload{
		Release:      &wire.HelmRelease{ReleaseName: "api", Namespace: "prod"},
		Dependencies: twoDependencies(),
	})

	result, err := agent.NewExecutor(helm, tofu).Execute(context.Background(), cmd, func(wire.LogPayload) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure")
	}
	if !slices.Equal(calls, []string{"uninstall prod/api"}) {
		t.Errorf("calls = %q, want only the uninstall", calls)
	}
}

func TestExecute_ServiceDelete_StopsAtFirstFailedDestroyAndNamesIt(t *testing.T) {
	var calls []string
	helm, tofu := recordingRunners(&calls, "destroy /states/e/api/cache/terraform.tfstate")
	cmd := serviceDeleteCommand(t, wire.ServiceDeletePayload{Dependencies: twoDependencies()})

	result, err := agent.NewExecutor(helm, tofu).Execute(context.Background(), cmd, func(wire.LogPayload) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(result.Error, `destroy dependency "cache"`) {
		t.Errorf("error should name the dependency: %s", result.Error)
	}
	if slices.Contains(calls, "init /mods/postgres") {
		t.Errorf("went on after the failure: %q", calls)
	}
}
