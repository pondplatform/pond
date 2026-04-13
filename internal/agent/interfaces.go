package agent

import (
	"context"
	"io"
)

// AgentConnection represents the persistent connection to the server.
type AgentConnection interface {
	Connect(ctx context.Context) error
	// SendReady tells the server the agent is ready for its first command.
	SendReady(ctx context.Context) error
	// SendAck notifies the server that execution of cmd has started.
	SendAck(ctx context.Context, cmd *Command) error
	// ReceiveMessage reads the next envelope from the server.
	ReceiveMessage(ctx context.Context) (*Envelope, error)
	SendResult(ctx context.Context, result *CommandResult) error
	SendLog(ctx context.Context, entry LogEntry) error
	Close() error
}

// CommandExecutor dispatches and runs agent commands.
type CommandExecutor interface {
	Execute(ctx context.Context, cmd *Command, logSink func(LogEntry)) (*CommandResult, error)
}

// HelmRunner wraps Helm CLI operations.
type HelmRunner interface {
	Upgrade(ctx context.Context, req HelmUpgradeRequest, logW io.Writer) error
}

type HelmUpgradeRequest struct {
	ReleaseName string
	Namespace   string
	ChartPath   string
	Values      []byte
}

// TofuRunner wraps OpenTofu CLI operations.
type TofuRunner interface {
	Init(ctx context.Context, workDir string, logW io.Writer) error
	Apply(ctx context.Context, workDir string, vars map[string]string, logW io.Writer) error
	Output(ctx context.Context, workDir string) (map[string]any, error)
	Destroy(ctx context.Context, workDir string, vars map[string]string, logW io.Writer) error
}
