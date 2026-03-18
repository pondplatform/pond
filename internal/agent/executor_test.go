package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/agent"
)

// --- mocks ---

type mockHelmRunner struct {
	upgradeFn func(ctx context.Context, req agent.HelmUpgradeRequest, logW io.Writer) error
}

func (m *mockHelmRunner) Upgrade(ctx context.Context, req agent.HelmUpgradeRequest, logW io.Writer) error {
	return m.upgradeFn(ctx, req, logW)
}

type mockTofuRunner struct {
	initFn    func(ctx context.Context, workDir string, logW io.Writer) error
	applyFn   func(ctx context.Context, workDir string, vars map[string]string, logW io.Writer) error
	outputFn  func(ctx context.Context, workDir string) (map[string]any, error)
	destroyFn func(ctx context.Context, workDir string, vars map[string]string, logW io.Writer) error
}

func (m *mockTofuRunner) Init(ctx context.Context, workDir string, logW io.Writer) error {
	return m.initFn(ctx, workDir, logW)
}
func (m *mockTofuRunner) Apply(ctx context.Context, workDir string, vars map[string]string, logW io.Writer) error {
	return m.applyFn(ctx, workDir, vars, logW)
}
func (m *mockTofuRunner) Output(ctx context.Context, workDir string) (map[string]any, error) {
	return m.outputFn(ctx, workDir)
}
func (m *mockTofuRunner) Destroy(ctx context.Context, workDir string, vars map[string]string, logW io.Writer) error {
	return m.destroyFn(ctx, workDir, vars, logW)
}

// --- tests ---

func TestExecute_HelmUpgrade_CallsRunner(t *testing.T) {
	called := false
	helm := &mockHelmRunner{
		upgradeFn: func(_ context.Context, req agent.HelmUpgradeRequest, logW io.Writer) error {
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

	payload, _ := json.Marshal(agent.HelmUpgradeRequest{
		ReleaseName: "my-release",
		Namespace:   "default",
		ChartPath:   "./chart",
		Values:      []byte("key: value\n"),
	})

	cmd := &agent.Command{
		ID:      uuid.New(),
		Type:    agent.CommandHelmUpgrade,
		Payload: payload,
	}

	var logLines []string
	logSink := func(e agent.LogEntry) { logLines = append(logLines, e.Line) }

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
		applyFn: func(_ context.Context, workDir string, vars map[string]string, logW io.Writer) error {
			applyCalled = true
			_, _ = io.WriteString(logW, "applying\n")
			return nil
		},
		outputFn: func(_ context.Context, workDir string) (map[string]any, error) {
			return map[string]any{"endpoint": "https://example.com"}, nil
		},
	}

	exec := agent.NewExecutor(&mockHelmRunner{}, tofu)

	type applyPayload struct {
		WorkDir string            `json:"workDir"`
		Vars    map[string]string `json:"vars"`
	}
	payload, _ := json.Marshal(applyPayload{WorkDir: "/tmp/tf", Vars: map[string]string{"region": "us-east-1"}})

	cmd := &agent.Command{
		ID:      uuid.New(),
		Type:    agent.CommandTofuApply,
		Payload: payload,
	}

	var logLines []string
	result, err := exec.Execute(context.Background(), cmd, func(e agent.LogEntry) {
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
	cmd := &agent.Command{ID: uuid.New(), Type: "unknown.command", Payload: json.RawMessage("{}")}

	result, err := exec.Execute(context.Background(), cmd, func(agent.LogEntry) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for unknown command type")
	}
}
