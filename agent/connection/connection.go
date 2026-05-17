package connection

import (
	"context"

	"github.com/pondplatform/pond/agent/internal/agent"
	"github.com/pondplatform/pond/shared/agent/wire"
)

// AgentConnection is the public interface for communicating with the pond server over WebSocket.
type AgentConnection interface {
	Connect(ctx context.Context) error
	SendReady(ctx context.Context) error
	SendAck(ctx context.Context, cmd *wire.CommandPayload) error
	ReceiveMessage(ctx context.Context) (*wire.Envelope, error)
	SendResult(ctx context.Context, result *wire.ResultPayload) error
	SendLog(ctx context.Context, entry wire.LogPayload) error
	Close() error
}

// New creates a new AgentConnection that connects to serverAddr using the given bearer token.
func New(serverAddr, token string) AgentConnection {
	return agent.NewConnection(serverAddr, token)
}
