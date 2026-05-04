//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/agent"
	"github.com/pondplatform/pond/internal/common/wire"
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
	conn       agent.AgentConnection

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
	a.conn = agent.NewConnection(a.serverAddr, a.token)
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

// BlockingBehavior creates a behavior that blocks on a channel before returning.
// Useful for testing agent disconnect scenarios.
func BlockingBehavior(unblock <-chan struct{}) Behavior {
	return Behavior{
		Handler: func(cmd *wire.CommandPayload) *wire.ResultPayload {
			<-unblock
			return &wire.ResultPayload{
				CommandID: cmd.ID,
				Success:   true,
			}
		},
	}
}

// CommandCountBehavior tracks the number of commands received and can fail
// specific command indices.
func CommandCountBehavior(failIndices map[int]string) Behavior {
	var count int
	var mu sync.Mutex
	return Behavior{
		Handler: func(cmd *wire.CommandPayload) *wire.ResultPayload {
			mu.Lock()
			idx := count
			count++
			mu.Unlock()

			if errMsg, shouldFail := failIndices[idx]; shouldFail {
				return &wire.ResultPayload{
					CommandID: cmd.ID,
					Success:   false,
					Error:     errMsg,
				}
			}
			return &wire.ResultPayload{
				CommandID: cmd.ID,
				Success:   true,
			}
		},
	}
}

// Used by tests to generate stable output for tofu commands
func MakeTofuOutput(host string, port int) json.RawMessage {
	output := map[string]any{
		"host": host,
		"port": port,
	}
	data, _ := json.Marshal(output)
	return data
}

// Compile-time check that FakeAgent uses the same interface concepts
var _ = uuid.New // ensure uuid is used
