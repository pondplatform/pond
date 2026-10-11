//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	agentconn "github.com/pondplatform/pond/agent/connection"
	"github.com/pondplatform/pond/shared/agent/wire"
)

// Behavior configures how the FakeAgent responds to commands.
type Behavior struct {
	// Handler is called for each command received.
	// Return a result to send back to the server.
	// If nil, DefaultBehavior is used.
	Handler func(cmd *wire.CommandPayload) *wire.ResultPayload

	// Logs are sent as log frames before the result.
	Logs []string

	// PreResultDelay introduces a pause before sending the result.
	PreResultDelay time.Duration

	// Gate, if set, holds every command after its ack until the channel is
	// closed, so a test can act while the command is in flight and then let
	// the result arrive late. A gate that is never closed means "acked, never
	// replies".
	Gate <-chan struct{}
}

// DefaultBehavior succeeds every command immediately with empty output.
var DefaultBehavior = Behavior{
	Handler: func(cmd *wire.CommandPayload) *wire.ResultPayload {
		return &wire.ResultPayload{
			CommandID: cmd.ID,
			Success:   true,
		}
	},
}

// FailingBehavior fails every command with the given error message.
func FailingBehavior(errMsg string) Behavior {
	return Behavior{
		Handler: func(cmd *wire.CommandPayload) *wire.ResultPayload {
			return &wire.ResultPayload{
				CommandID: cmd.ID,
				Success:   false,
				Error:     errMsg,
			}
		},
	}
}

// FakeAgent is a test WebSocket client that mimics the real agent protocol.
type FakeAgent struct {
	serverAddr string
	token      string
	behavior   Behavior
	conn       agentconn.AgentConnection

	mu       sync.Mutex
	Commands []*wire.CommandPayload // commands received, in order
	done     chan struct{}
	cancel   context.CancelFunc
}

// NewFakeAgent creates a new fake agent that will connect to the given server.
func NewFakeAgent(serverAddr, token string, b Behavior) *FakeAgent {
	if b.Handler == nil {
		b.Handler = DefaultBehavior.Handler
	}
	return &FakeAgent{
		serverAddr: serverAddr,
		token:      token,
		behavior:   b,
		done:       make(chan struct{}),
	}
}

// Connect establishes the WebSocket connection and sends the ready message.
func (a *FakeAgent) Connect(ctx context.Context) error {
	a.conn = agentconn.New(a.serverAddr, a.token)
	if err := a.conn.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if err := a.conn.SendReady(ctx); err != nil {
		a.conn.Close()
		return fmt.Errorf("send ready: %w", err)
	}
	return nil
}

// Run starts the message loop. It blocks until the context is cancelled or Stop is called.
func (a *FakeAgent) Run(ctx context.Context) error {
	ctx, a.cancel = context.WithCancel(ctx)
	defer close(a.done)

	for {
		msg, err := a.conn.ReceiveMessage(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("receive message: %w", err)
			}
		}

		switch msg.Type {
		case "command":
			var cmd wire.CommandPayload
			if err := json.Unmarshal(msg.Data, &cmd); err != nil {
				continue
			}

			a.mu.Lock()
			a.Commands = append(a.Commands, &cmd)
			a.mu.Unlock()

			// Send ack
			if err := a.conn.SendAck(ctx, &cmd); err != nil {
				return fmt.Errorf("send ack: %w", err)
			}

			// Send logs if configured
			for _, line := range a.behavior.Logs {
				entry := wire.LogPayload{
					CommandID: cmd.ID,
					Line:      line,
					Timestamp: time.Now(),
					Stream:    "stdout",
				}
				if err := a.conn.SendLog(ctx, entry); err != nil {
					return fmt.Errorf("send log: %w", err)
				}
			}

			if a.behavior.Gate != nil {
				select {
				case <-a.behavior.Gate:
				case <-ctx.Done():
					return nil
				}
			}

			// Optional delay
			if a.behavior.PreResultDelay > 0 {
				select {
				case <-time.After(a.behavior.PreResultDelay):
				case <-ctx.Done():
					return nil
				}
			}

			// Execute handler and send result
			result := a.behavior.Handler(&cmd)
			result.DeploymentID = cmd.DeploymentID
			if err := a.conn.SendResult(ctx, result); err != nil {
				return fmt.Errorf("send result: %w", err)
			}

		case "idle":
			// Server has no commands; continue waiting

		default:
			// Ignore unknown message types
		}
	}
}

// Stop gracefully stops the agent.
func (a *FakeAgent) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.conn != nil {
		a.conn.Close()
	}
	<-a.done
}

// CloseConnection closes the WebSocket connection without waiting for the run loop.
// This is useful for simulating abrupt disconnects in tests.
func (a *FakeAgent) CloseConnection() {
	if a.conn != nil {
		a.conn.Close()
	}
}

// ReceivedCommands returns a copy of all commands received by this agent.
func (a *FakeAgent) ReceivedCommands() []*wire.CommandPayload {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]*wire.CommandPayload, len(a.Commands))
	copy(result, a.Commands)
	return result
}

// WaitForCommands blocks until the agent has received at least n commands,
// and fails the test if that takes longer than 5 seconds.
func (a *FakeAgent) WaitForCommands(ctx context.Context, t *testing.T, n int) []*wire.CommandPayload {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		cmds := a.ReceivedCommands()
		if len(cmds) >= n {
			return cmds
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake agent received %d commands within 5s, want at least %d", len(cmds), n)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context done while waiting for %d commands: %v", n, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// NewGate returns a gate for Behavior.Gate and the function that opens it.
// The gate is also opened when the test ends.
func NewGate(t *testing.T) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(ch) }) }
	t.Cleanup(release)
	return ch, release
}

// TofuOutputBehavior returns a behavior that succeeds tofu.apply commands
// with the given output JSON, and succeeds all other commands with empty output.
func TofuOutputBehavior(output map[string]any) Behavior {
	outputJSON, _ := json.Marshal(output)
	return Behavior{
		Handler: func(cmd *wire.CommandPayload) *wire.ResultPayload {
			result := &wire.ResultPayload{
				CommandID: cmd.ID,
				Success:   true,
			}
			if cmd.Type == wire.CommandTofuApply {
				result.Output = outputJSON
			}
			return result
		},
	}
}

// PerCommandBehavior returns a behavior that uses different handlers per command type.
func PerCommandBehavior(handlers map[wire.CommandType]func(cmd *wire.CommandPayload) *wire.ResultPayload) Behavior {
	return Behavior{
		Handler: func(cmd *wire.CommandPayload) *wire.ResultPayload {
			if h, ok := handlers[cmd.Type]; ok {
				return h(cmd)
			}
			// Default: succeed
			return &wire.ResultPayload{
				CommandID: cmd.ID,
				Success:   true,
			}
		},
	}
}

