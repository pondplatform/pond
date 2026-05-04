package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/agent"
	"github.com/pondplatform/pond/internal/common/wire"
)

// --- mocks ---

type mockHelmRunner struct {
	upgradeFn func(ctx context.Context, req wire.HelmUpgradePayload, logW io.Writer) error
}

func (m *mockHelmRunner) Upgrade(ctx context.Context, req wire.HelmUpgradePayload, logW io.Writer) error {
	return m.upgradeFn(ctx, req, logW)
}

type mockTofuRunner struct {
	initFn    func(ctx context.Context, workDir string, logW io.Writer) error
	applyFn   func(ctx context.Context, workDir string, statePath string, vars map[string]any, logW io.Writer) error
	outputFn  func(ctx context.Context, workDir string, statePath string) (map[string]any, error)
	destroyFn func(ctx context.Context, workDir string, statePath string, vars map[string]any, logW io.Writer) error
}

func (m *mockTofuRunner) Init(ctx context.Context, workDir string, logW io.Writer) error {
	return m.initFn(ctx, workDir, logW)
}
func (m *mockTofuRunner) Apply(ctx context.Context, workDir string, statePath string, vars map[string]any, logW io.Writer) error {
	return m.applyFn(ctx, workDir, statePath, vars, logW)
}
func (m *mockTofuRunner) Output(ctx context.Context, workDir string, statePath string) (map[string]any, error) {
	return m.outputFn(ctx, workDir, statePath)
}
func (m *mockTofuRunner) Destroy(ctx context.Context, workDir string, statePath string, vars map[string]any, logW io.Writer) error {
	return m.destroyFn(ctx, workDir, statePath, vars, logW)
}

// --- tests ---

func TestExecute_HelmUpgrade_CallsRunner(t *testing.T) {
	called := false
	helm := &mockHelmRunner{
		upgradeFn: func(_ context.Context, req wire.HelmUpgradePayload, logW io.Writer) error {
			called = true
			if req.ReleaseName != "my-release" {
				t.Errorf("unexpected release name: %s", req.ReleaseName)
			}
			// Simulate output so logSink receives a line.
			_, _ = io.WriteString(logW, "deploying\n")
			return nil
		},
	}

	exec := agent.NewExecutor(helm, &mockTofuRunner{})

	payload, _ := json.Marshal(wire.HelmUpgradePayload{
		ReleaseName: "my-release",
		Namespace:   "default",
		ChartPath:   "./chart",
		Values:      []byte("key: value\n"),
	})

	cmd := &wire.CommandPayload{
		ID:      uuid.New(),
		Type:    wire.CommandHelmUpgrade,
		Payload: payload,
	}

	var logLines []string
	logSink := func(e wire.LogPayload) { logLines = append(logLines, e.Line) }

	result, err := exec.Execute(context.Background(), cmd, logSink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("helm runner not called")
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if len(logLines) == 0 {
		t.Fatal("expected log lines, got none")
	}
}
func TestExecute_TofuApply_CallsRunners(t *testing.T) {
	initCalled, applyCalled := false, false

	tofu := &mockTofuRunner{
		initFn: func(_ context.Context, workDir string, logW io.Writer) error {
			initCalled = true
			_, _ = io.WriteString(logW, "initializing\n")
			return nil
		},
		applyFn: func(_ context.Context, workDir string, statePath string, vars map[string]any, logW io.Writer) error {
			applyCalled = true
			if statePath != "states/my-service/my-dep/terraform.tfstate" {
				t.Errorf("unexpected state path: %s", statePath)
			}
			_, _ = io.WriteString(logW, "applying\n")
			return nil
		},
		outputFn: func(_ context.Context, workDir string, statePath string) (map[string]any, error) {
			return map[string]any{"endpoint": "https://example.com"}, nil
		},
	}

	exec := agent.NewExecutor(&mockHelmRunner{}, tofu)

	payload, _ := json.Marshal(wire.TofuApplyPayload{
		WorkDir:   "./tofu",
		StatePath: "states/my-service/my-dep/terraform.tfstate",
		Vars:      map[string]any{"key": "value"},
	})
	cmd := &wire.CommandPayload{
		ID:      uuid.New(),
		Type:    wire.CommandTofuApply,
		Payload: payload,
	}

	var logLines []string
	result, err := exec.Execute(context.Background(), cmd, func(e wire.LogPayload) {
		logLines = append(logLines, e.Line)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !initCalled || !applyCalled {
		t.Fatal("expected both Init and Apply to be called")
	}
	if !result.Success {
		t.Fatalf("expected success: %s", result.Error)
	}
	if len(logLines) < 2 {
		t.Fatalf("expected ≥2 log lines, got %d", len(logLines))
	}
}
func TestExecute_UnknownType_ReturnsError(t *testing.T) {
	exec := agent.NewExecutor(&mockHelmRunner{}, &mockTofuRunner{})
	cmd := &wire.CommandPayload{ID: uuid.New(), Type: "unknown.command", Payload: json.RawMessage("{}")}

	result, err := exec.Execute(context.Background(), cmd, func(wire.LogPayload) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for unknown command type")
	}
}
