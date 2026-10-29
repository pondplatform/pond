package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"
	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/agent/wire"
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
	case wire.CommandServiceDelete:
		result, err = e.executeServiceDelete(ctx, cmd, logSink)
	default:
		return &wire.ResultPayload{
			CommandID: cmd.ID,
			Success:   false,
			Error:     fmt.Sprintf("this agent does not support command type %q; the agent may be older than the server", cmd.Type),
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
		return nil, payloadError(cmd.Type, err)
	}

	lw := newLogWriter(cmd.ID, logSink)
	defer lw.Close()

	if err := e.helmRunner.Upgrade(ctx, req, lw); err != nil {
		return nil, err
	}

	return &wire.ResultPayload{
		CommandID: cmd.ID,
		Success:   true,
	}, nil
}

func (e *executor) executeTofuApply(ctx context.Context, cmd *wire.CommandPayload, logSink func(wire.LogPayload)) (*wire.ResultPayload, error) {
	var payload wire.TofuApplyPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return nil, payloadError(cmd.Type, err)
	}

	lw := newLogWriter(cmd.ID, logSink)
	defer lw.Close()

	if err := e.tofuRunner.Init(ctx, payload.WorkDir, lw); err != nil {
		return nil, err
	}

	if err := e.tofuRunner.Apply(ctx, payload.WorkDir, payload.StatePath, payload.Vars, lw); err != nil {
		return nil, err
	}

	outputs, err := e.tofuRunner.Output(ctx, payload.WorkDir, payload.StatePath)
	if err != nil {
		return nil, err
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
		return nil, payloadError(cmd.Type, err)
	}

	outputs, err := e.tofuRunner.Output(ctx, payload.WorkDir, payload.StatePath)
	if err != nil {
		return nil, err
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

// executeServiceDelete uninstalls the release before destroying anything, so
// no pod of the service still uses a dependency when it goes.
func (e *executor) executeServiceDelete(ctx context.Context, cmd *wire.CommandPayload, logSink func(wire.LogPayload)) (*wire.ResultPayload, error) {
	var payload wire.ServiceDeletePayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return nil, payloadError(cmd.Type, err)
	}

	lw := newLogWriter(cmd.ID, logSink)
	defer lw.Close()

	if payload.Release != nil {
		if err := e.helmRunner.Uninstall(ctx, *payload.Release, lw); err != nil {
			return nil, err
		}
	}
	for _, dep := range payload.Dependencies {
		if err := e.tofuRunner.Init(ctx, dep.WorkDir, lw); err != nil {
			return nil, fmt.Errorf("destroy dependency %q: %w", dep.Name, err)
		}
		if err := e.tofuRunner.Destroy(ctx, dep.WorkDir, dep.StatePath, dep.Vars, lw); err != nil {
			return nil, fmt.Errorf("destroy dependency %q: %w", dep.Name, err)
		}
	}

	return &wire.ResultPayload{
		CommandID: cmd.ID,
		Success:   true,
	}, nil
}

func payloadError(cmdType wire.CommandType, err error) error {
	return fmt.Errorf("agent could not decode the %s payload (%w); the agent and server versions may differ", cmdType, err)
}

// logWriter is a line-buffered writer that sends each line to logSink.
type logWriter struct {
	cmdID   uuid.UUID
	logSink func(wire.LogPayload)
	pw      *io.PipeWriter
	done    chan struct{}
}

func newLogWriter(cmdID uuid.UUID, logSink func(wire.LogPayload)) *logWriter {
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
			if err := scanner.Err(); err != nil {
				logSink(wire.LogPayload{
					CommandID: lw.cmdID,
					Line:      "~~~~FAILED TO READ COMMAND OUTPUT~~~~",
					Timestamp: time.Now(),
					Stream:    "stderr",
				})
				break;
			}
			logSink(wire.LogPayload{
				CommandID: lw.cmdID,
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
