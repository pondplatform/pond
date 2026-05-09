package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/pondplatform/pond/internal/common/wire"
)

const CommandIdTag = "command_id"

type executor struct {
	helmRunner HelmRunner
	tofuRunner TofuRunner
}

func NewExecutor(helmRunner HelmRunner, tofuRunner TofuRunner) CommandExecutor {
	return &executor{
		helmRunner: helmRunner,
		tofuRunner: tofuRunner,
	}
}

func (e *executor) Execute(ctx context.Context, cmd *wire.CommandPayload, logSink func(wire.LogPayload)) (*wire.ResultPayload, error) {
	var result *wire.ResultPayload
	var err error

	switch cmd.Type {
	case wire.CommandHelmUpgrade:
		result, err = e.executeHelmUpgrade(ctx, cmd, logSink)
	case wire.CommandTofuApply:
		result, err = e.executeTofuApply(ctx, cmd, logSink)
	case wire.CommandTofuOutput:
		result, err = e.executeTofuOutput(ctx, cmd)
	default:
		return &wire.ResultPayload{
			CommandID: cmd.ID,
			Success:   false,
			Error:     fmt.Sprintf("unknown command type: %s", cmd.Type),
		}, nil
	}

	if err != nil {
		return &wire.ResultPayload{
			CommandID: cmd.ID,
			Success:   false,
			Error:     err.Error(),
		}, nil
	}

	return result, nil
}

func (e *executor) executeHelmUpgrade(ctx context.Context, cmd *wire.CommandPayload, logSink func(wire.LogPayload)) (*wire.ResultPayload, error) {
	var req wire.HelmUpgradePayload
	if err := json.Unmarshal(cmd.Payload, &req); err != nil {
		return nil, fmt.Errorf("unmarshal helm upgrade request: %w", err)
	}

	lw := newLogWriter(cmd.ID.String(), logSink)
	defer lw.Close()

	if err := e.helmRunner.Upgrade(ctx, req, lw); err != nil {
		return nil, fmt.Errorf("helm upgrade: %w", err)
	}

	return &wire.ResultPayload{
		CommandID: cmd.ID,
		Success:   true,
	}, nil
}

func (e *executor) executeTofuApply(ctx context.Context, cmd *wire.CommandPayload, logSink func(wire.LogPayload)) (*wire.ResultPayload, error) {
	var payload wire.TofuApplyPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal tofu apply request: %w", err)
	}

	lw := newLogWriter(cmd.ID.String(), logSink)
	defer lw.Close()

	if err := e.tofuRunner.Init(ctx, payload.WorkDir, lw); err != nil {
		return nil, fmt.Errorf("tofu init: %w", err)
	}

	if err := e.tofuRunner.Apply(ctx, payload.WorkDir, payload.StatePath, payload.Vars, lw); err != nil {
		return nil, fmt.Errorf("tofu apply: %w", err)
	}

	outputs, err := e.tofuRunner.Output(ctx, payload.WorkDir, payload.StatePath)
	if err != nil {
		return nil, fmt.Errorf("tofu output: %w", err)
	}

	outputJSON, err := json.Marshal(outputs)
	if err != nil {
		return nil, fmt.Errorf("marshal outputs: %w", err)
	}

	return &wire.ResultPayload{
		CommandID: cmd.ID,
		Success:   true,
		Output:    outputJSON,
	}, nil
}

func (e *executor) executeTofuOutput(ctx context.Context, cmd *wire.CommandPayload) (*wire.ResultPayload, error) {
	var payload wire.TofuOutputPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal tofu output request: %w", err)
	}

	outputs, err := e.tofuRunner.Output(ctx, payload.WorkDir, payload.StatePath)
	if err != nil {
		return nil, fmt.Errorf("tofu output: %w", err)
	}

	outputJSON, err := json.Marshal(outputs)
	if err != nil {
		return nil, fmt.Errorf("marshal outputs: %w", err)
	}

	return &wire.ResultPayload{
		CommandID: cmd.ID,
		Success:   true,
		Output:    outputJSON,
	}, nil
}

// logWriter is a line-buffered writer that sends each line to logSink.
type logWriter struct {
	cmdID   string
	logSink func(wire.LogPayload)
	pw      *io.PipeWriter
	done    chan struct{}
}

func newLogWriter(cmdID string, logSink func(wire.LogPayload)) *logWriter {
	pr, pw := io.Pipe()
	lw := &logWriter{
		cmdID:   cmdID,
		logSink: logSink,
		pw:      pw,
		done:    make(chan struct{}),
	}
	go func() {
		defer close(lw.done)
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			logSink(wire.LogPayload{
				Line:      scanner.Text(),
				Timestamp: time.Now(),
				Stream:    "stdout",
			})
		}
	}()
	return lw
}

func (lw *logWriter) Write(p []byte) (int, error) {
	slog.Default().Info("Command log: "+string(p), CommandIdTag, lw.cmdID)
	return lw.pw.Write(p)
}

func (lw *logWriter) Close() error {
	err := lw.pw.Close()
	<-lw.done
	return err
}
