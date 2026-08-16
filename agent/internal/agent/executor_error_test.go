package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/agent/internal/agent"
	"github.com/pondplatform/pond/shared/agent/wire"
)

func TestExecute_HelmUpgrade_RunnerErrorReturnsFailure(t *testing.T) {
	helm := &mockHelmRunner{
		upgradeFn: func(_ context.Context, _ wire.HelmUpgradePayload, _ io.Writer) error {
			return errors.New("tiller unavailable")
		},
	}
	exec := agent.NewExecutor(helm, &mockTofuRunner{})

	payload, _ := json.Marshal(wire.HelmUpgradePayload{
		ReleaseName: "svc",
		Namespace:   "default",
		ChartPath:   "./chart",
		Values:      []byte("key: value\n"),
	})
	cmd := &wire.CommandPayload{ID: uuid.New(), Type: wire.CommandHelmUpgrade, Payload: payload}

	result, err := exec.Execute(context.Background(), cmd, func(wire.LogPayload) {})
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure when helm runner errors")
	}
	if result.Error == "" {
		t.Error("expected non-empty error message in result")
	}
}

func TestExecute_TofuApply_InitErrorReturnsFailure(t *testing.T) {
	var applyCalled bool
	tofu := &mockTofuRunner{
		initFn: func(_ context.Context, _ string, _ io.Writer) error {
			return errors.New("init failed")
		},
		applyFn: func(_ context.Context, _ string, _ string, _ map[string]any, _ io.Writer) error {
			applyCalled = true
			return nil
		},
	}
	exec := agent.NewExecutor(&mockHelmRunner{}, tofu)

	payload, _ := json.Marshal(wire.TofuApplyPayload{
		WorkDir:   "./tofu",
		StatePath: "states/svc/dep/terraform.tfstate",
		Vars:      map[string]any{},
	})
	cmd := &wire.CommandPayload{ID: uuid.New(), Type: wire.CommandTofuApply, Payload: payload}

	result, err := exec.Execute(context.Background(), cmd, func(wire.LogPayload) {})
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure when tofu init errors")
	}
	if applyCalled {
		t.Error("expected Apply not to be called when Init fails")
	}
}

func TestExecute_TofuApply_ApplyErrorReturnsFailure(t *testing.T) {
	tofu := &mockTofuRunner{
		initFn: func(_ context.Context, _ string, _ io.Writer) error { return nil },
		applyFn: func(_ context.Context, _ string, _ string, _ map[string]any, _ io.Writer) error {
			return errors.New("apply failed")
		},
	}
	exec := agent.NewExecutor(&mockHelmRunner{}, tofu)

	payload, _ := json.Marshal(wire.TofuApplyPayload{
		WorkDir:   "./tofu",
		StatePath: "states/svc/dep/terraform.tfstate",
	})
	cmd := &wire.CommandPayload{ID: uuid.New(), Type: wire.CommandTofuApply, Payload: payload}

	result, err := exec.Execute(context.Background(), cmd, func(wire.LogPayload) {})
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure when tofu apply errors")
	}
	if result.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestExecute_TofuOutput_ReturnsOutputJSON(t *testing.T) {
	tofu := &mockTofuRunner{
		outputFn: func(_ context.Context, _ string, _ string) (map[string]any, error) {
			return map[string]any{"host": "db.internal", "port": 5432}, nil
		},
	}
	exec := agent.NewExecutor(&mockHelmRunner{}, tofu)

	payload, _ := json.Marshal(wire.TofuOutputPayload{
		WorkDir:   "./tofu",
		StatePath: "states/svc/dep/terraform.tfstate",
	})
	cmd := &wire.CommandPayload{ID: uuid.New(), Type: wire.CommandTofuOutput, Payload: payload}

	result, err := exec.Execute(context.Background(), cmd, func(wire.LogPayload) {})
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	var outputs map[string]any
	if err := json.Unmarshal(result.Output, &outputs); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if outputs["host"] != "db.internal" {
		t.Errorf("expected host='db.internal', got %v", outputs["host"])
	}
}

func TestExecute_TofuOutput_RunnerErrorReturnsFailure(t *testing.T) {
	tofu := &mockTofuRunner{
		outputFn: func(_ context.Context, _ string, _ string) (map[string]any, error) {
			return nil, errors.New("state not found")
		},
	}
	exec := agent.NewExecutor(&mockHelmRunner{}, tofu)

	payload, _ := json.Marshal(wire.TofuOutputPayload{WorkDir: "./tofu", StatePath: "state.tfstate"})
	cmd := &wire.CommandPayload{ID: uuid.New(), Type: wire.CommandTofuOutput, Payload: payload}

	result, err := exec.Execute(context.Background(), cmd, func(wire.LogPayload) {})
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure when output runner errors")
	}
}
