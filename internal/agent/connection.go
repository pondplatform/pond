package agent

import (
	"context"
	"fmt"
	"sync"
)

type connection struct {
	serverAddr string
	token      string
	mu         sync.Mutex
	connected  bool
}

func NewConnection(serverAddr, token string) AgentConnection {
	return &connection{
		serverAddr: serverAddr,
		token:      token,
	}
}

func (c *connection) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// TODO: Establish gRPC stream to server.
	c.connected = true
	return nil
}

func (c *connection) ReceiveCommand(ctx context.Context) (*Command, error) {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return nil, fmt.Errorf("not connected")
	}
	c.mu.Unlock()

	// TODO: Read from gRPC stream.
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *connection) SendResult(ctx context.Context, result *CommandResult) error {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	c.mu.Unlock()

	// TODO: Send via gRPC stream.
	return nil
}

func (c *connection) SendLog(ctx context.Context, entry LogEntry) error {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	c.mu.Unlock()

	// TODO: Send via gRPC stream.
	return nil
}

func (c *connection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.connected = false
	// TODO: Close gRPC stream.
	return nil
}
