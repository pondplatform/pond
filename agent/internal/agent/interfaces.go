package agent

import (
	"context"
	"io"

	"github.com/pondplatform/pond/shared/agent/wire"
)

// AgentConnection represents the persistent connection to the server.
type AgentConnection interface {
	Connect(ctx context.Context) error
	// SendReady tells the server the agent is ready for its first command.
	SendReady(ctx context.Context) error
	// SendAck notifies the server that execution of cmd has started.
	SendAck(ctx context.Context, cmd *wire.CommandPayload) error
	// ReceiveMessage reads the next envelope from the server.
	ReceiveMessage(ctx context.Context) (*wire.Envelope, error)
	SendResult(ctx context.Context, result *wire.ResultPayload) error
	SendLog(ctx context.Context, entry wire.LogPayload) error
	Close() error
}

// CommandExecutor dispatches and runs agent commands.
type CommandExecutor interface {
	Execute(ctx context.Context, cmd *wire.CommandPayload, logSink func(wire.LogPayload)) (*wire.ResultPayload, error)
}

// HelmRunner wraps Helm CLI operations.
type HelmRunner interface {
	Upgrade(ctx context.Context, req wire.HelmUpgradePayload, logW io.Writer) error
}

// TofuRunner wraps OpenTofu CLI operations.
type TofuRunner interface {
	Init(ctx context.Context, workDir string, logW io.Writer) error
	Apply(ctx context.Context, workDir string, statePath string, vars map[string]any, logW io.Writer) error
	Output(ctx context.Context, workDir string, statePath string) (map[string]any, error)
	Destroy(ctx context.Context, workDir string, statePath string, vars map[string]any, logW io.Writer) error
}
