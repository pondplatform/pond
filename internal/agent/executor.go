package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

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

func (e *executor) Execute(ctx context.Context, cmd *Command, logSink func(LogEntry)) (*CommandResult, error) {
	var result *CommandResult
	var err error

	switch cmd.Type {
	case CommandHelmUpgrade:
		result, err = e.executeHelmUpgrade(ctx, cmd, logSink)
	case CommandTofuApply:
		result, err = e.executeTofuApply(ctx, cmd, logSink)
	case CommandTofuOutput:
		result, err = e.executeTofuOutput(ctx, cmd)
	default:
		return &CommandResult{
			CommandID: cmd.ID,
			Success:   false,
			Error:     fmt.Sprintf("unknown command type: %s", cmd.Type),
		}, nil
	}

	if err != nil {
		return &CommandResult{
			CommandID: cmd.ID,
			Success:   false,
			Error:     err.Error(),
		}, nil
	}

	return result, nil
}

func (e *executor) executeHelmUpgrade(ctx context.Context, cmd *Command, logSink func(LogEntry)) (*CommandResult, error) {
	var req HelmUpgradeRequest
	if err := json.Unmarshal(cmd.Payload, &req); err != nil {
		return nil, fmt.Errorf("unmarshal helm upgrade request: %w", err)
	}

	lw := newLogWriter(cmd.ID.String(), logSink)
	defer lw.Close()

	if err := e.helmRunner.Upgrade(ctx, req, lw); err != nil {
		return nil, fmt.Errorf("helm upgrade: %w", err)
	}

	return &CommandResult{
		CommandID: cmd.ID,
		Success:   true,
	}, nil
}

func (e *executor) executeTofuApply(ctx context.Context, cmd *Command, logSink func(LogEntry)) (*CommandResult, error) {
	var payload struct {
		WorkDir string            `json:"workDir"`
		Vars    map[string]string `json:"vars"`
	}
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal tofu apply request: %w", err)
	}

	lw := newLogWriter(cmd.ID.String(), logSink)
	defer lw.Close()

	if err := e.tofuRunner.Init(ctx, payload.WorkDir, lw); err != nil {
		return nil, fmt.Errorf("tofu init: %w", err)
	}

	if err := e.tofuRunner.Apply(ctx, payload.WorkDir, payload.Vars, lw); err != nil {
		return nil, fmt.Errorf("tofu apply: %w", err)
	}

	outputs, err := e.tofuRunner.Output(ctx, payload.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("tofu output: %w", err)
	}

	outputJSON, err := json.Marshal(outputs)
	if err != nil {
		return nil, fmt.Errorf("marshal outputs: %w", err)
	}

	return &CommandResult{
		CommandID: cmd.ID,
		Success:   true,
		Output:    outputJSON,
	}, nil
}

func (e *executor) executeTofuOutput(ctx context.Context, cmd *Command) (*CommandResult, error) {
	var payload struct {
		WorkDir string `json:"workDir"`
	}
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal tofu output request: %w", err)
	}

	outputs, err := e.tofuRunner.Output(ctx, payload.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("tofu output: %w", err)
	}

	outputJSON, err := json.Marshal(outputs)
	if err != nil {
		return nil, fmt.Errorf("marshal outputs: %w", err)
	}

	return &CommandResult{
		CommandID: cmd.ID,
		Success:   true,
		Output:    outputJSON,
	}, nil
}

// logWriter is a line-buffered writer that sends each line to logSink.
type logWriter struct {
	cmdID   string
	logSink func(LogEntry)
	pw      *io.PipeWriter
	done    chan struct{}
}

func newLogWriter(cmdID string, logSink func(LogEntry)) *logWriter {
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
			logSink(LogEntry{
				Line:      scanner.Text(),
				Timestamp: time.Now(),
				Stream:    "stdout",
			})
		}
	}()
	return lw
}

func (lw *logWriter) Write(p []byte) (int, error) {
	return lw.pw.Write(p)
}

func (lw *logWriter) Close() error {
	err := lw.pw.Close()
	<-lw.done
	return err
}
