package agent

import "context"

// AgentConnection represents the persistent connection to the server.
type AgentConnection interface {
	Connect(ctx context.Context) error
	ReceiveCommand(ctx context.Context) (*Command, error)
	SendResult(ctx context.Context, result *CommandResult) error
	SendLog(ctx context.Context, entry LogEntry) error
	Close() error
}

// CommandExecutor dispatches and runs agent commands.
type CommandExecutor interface {
	Execute(ctx context.Context, cmd *Command) (*CommandResult, error)
}

// HelmRunner wraps Helm CLI operations.
type HelmRunner interface {
	Upgrade(ctx context.Context, req HelmUpgradeRequest) error
}

type HelmUpgradeRequest struct {
	ReleaseName string
	Namespace   string
	ChartPath   string
	Values      []byte
}

// TofuRunner wraps OpenTofu CLI operations.
type TofuRunner interface {
	Init(ctx context.Context, workDir string) error
	Apply(ctx context.Context, workDir string, vars map[string]string) error
	Output(ctx context.Context, workDir string) (map[string]any, error)
	Destroy(ctx context.Context, workDir string, vars map[string]string) error
}
