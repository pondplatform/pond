//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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

	// Gate, if set, holds every command after it arrives until the channel is
	// closed, so a test can act while the command is in flight and then let
	// the result arrive late. A gate that is never closed means the agent
	// never replies.
	Gate <-chan struct{}

	// IgnoreCancel makes the agent finish a command even when the server
	// cancels it, like one that got the cancel too late.
	IgnoreCancel bool
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

// fakeAgentPollInterval is how soon an idle fake agent asks for work again.
const fakeAgentPollInterval = 50 * time.Millisecond

// FakeAgent is a test WebSocket client that mimics the real agent protocol.
type FakeAgent struct {
	serverAddr string
	token      string
	behavior   Behavior
	conn       agentconn.AgentConnection

	mu        sync.Mutex
	Commands  []*wire.CommandPayload // commands received, in order
	cancelled []uuid.UUID            // commands the server told it to cancel
	stops     map[uuid.UUID]chan struct{}
	done      chan struct{}
	cancel    context.CancelFunc
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
		stops:      make(map[uuid.UUID]chan struct{}),
		done:       make(chan struct{}),
	}
}

// Connect establishes the WebSocket connection and opens it like an agent
// that just started: a hello that holds nothing, then ready.
func (a *FakeAgent) Connect(ctx context.Context) error {
	a.conn = agentconn.New(a.serverAddr, a.token)
	if err := a.conn.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if err := a.conn.SendHello(ctx, wire.HelloPayload{Restarted: true}); err != nil {
		a.conn.Close()
		return fmt.Errorf("send hello: %w", err)
	}
	if err := a.conn.SendReady(ctx); err != nil {
		a.conn.Close()
		return fmt.Errorf("send ready: %w", err)
	}
	return nil
}

// Run starts the message loop. It blocks until the context is cancelled or Stop is called.
// Each command runs in its own goroutine, so the loop keeps reading and can
// receive a cancel for it.
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
		case wire.MessageCommand:
			var cmd wire.CommandPayload
			if err := json.Unmarshal(msg.Data, &cmd); err != nil {
				continue
			}
			stop := make(chan struct{})
			a.mu.Lock()
			a.Commands = append(a.Commands, &cmd)
			a.stops[cmd.ID] = stop
			a.mu.Unlock()
			go a.execute(ctx, &cmd, stop)

		case wire.MessageCancel:
			var cancel wire.CancelPayload
			if err := json.Unmarshal(msg.Data, &cancel); err != nil {
				continue
			}
			a.mu.Lock()
			a.cancelled = append(a.cancelled, cancel.CommandID)
			stop, running := a.stops[cancel.CommandID]
			delete(a.stops, cancel.CommandID)
			a.mu.Unlock()
			if running && !a.behavior.IgnoreCancel {
				close(stop)
			}

		case wire.MessageIdle:
			select {
			case <-time.After(fakeAgentPollInterval):
			case <-ctx.Done():
				return nil
			}
			if err := a.conn.SendReady(ctx); err != nil {
				return fmt.Errorf("send ready: %w", err)
			}
		}
	}
}

// execute plays out one command: logs, then the gate and delay, then the
// handler's result. A cancel cuts it short with a failed result.
func (a *FakeAgent) execute(ctx context.Context, cmd *wire.CommandPayload, stop <-chan struct{}) {
	defer func() {
		a.mu.Lock()
		delete(a.stops, cmd.ID)
		a.mu.Unlock()
	}()
	for _, line := range a.behavior.Logs {
		_ = a.conn.SendLog(ctx, wire.LogPayload{CommandID: cmd.ID, Line: line, Timestamp: time.Now(), Stream: "stdout"})
	}

	result := &wire.ResultPayload{CommandID: cmd.ID, Success: false, Error: "cancelled by the server"}
	if a.wait(ctx, a.behavior.Gate, stop) && a.wait(ctx, delay(a.behavior.PreResultDelay), stop) {
		result = a.behavior.Handler(cmd)
	}
	if ctx.Err() != nil {
		return
	}
	result.DeploymentID = cmd.DeploymentID
	if err := a.conn.SendResult(ctx, result); err != nil {
		return
	}
	_ = a.conn.SendReady(ctx)
}

// wait blocks until ready is closed, and reports false if the command was
// cancelled first. A nil ready does not block.
func (a *FakeAgent) wait(ctx context.Context, ready <-chan struct{}, stop <-chan struct{}) bool {
	if ready == nil {
		return true
	}
	select {
	case <-ready:
		return true
	case <-stop:
		return false
	case <-ctx.Done():
		return false
	}
}

func delay(d time.Duration) <-chan struct{} {
	if d <= 0 {
		return nil
	}
	ch := make(chan struct{})
	time.AfterFunc(d, func() { close(ch) })
	return ch
}

// CancelledCommands returns the commands the server told this agent to cancel.
func (a *FakeAgent) CancelledCommands() []uuid.UUID {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]uuid.UUID(nil), a.cancelled...)
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

// AgentSession is an agent connection the test drives by hand, for what the
// fake agent doesn't do: come back holding commands from an earlier
// connection, as the agent does after it lost one.
type AgentSession struct {
	t    *testing.T
	conn agentconn.AgentConnection
}

// ConnectAgent opens a connection for the cluster's agent and sends hello.
// The connection is closed when the test ends.
func (h *TestHarness) ConnectAgent(ctx context.Context, t *testing.T, token string, hello wire.HelloPayload) *AgentSession {
	t.Helper()
	conn := agentconn.New(h.WsAddr, token)
	if err := conn.Connect(ctx); err != nil {
		t.Fatalf("connect agent: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SendHello(ctx, hello); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	return &AgentSession{t: t, conn: conn}
}

// Ready asks for work and returns the server's answer. The server answers
// once it has handled everything sent before.
func (s *AgentSession) Ready(ctx context.Context) *wire.Envelope {
	s.t.Helper()
	if err := s.conn.SendReady(ctx); err != nil {
		s.t.Fatalf("send ready: %v", err)
	}
	return s.Receive(ctx)
}

// Result reports a command's result.
func (s *AgentSession) Result(ctx context.Context, result *wire.ResultPayload) {
	s.t.Helper()
	if err := s.conn.SendResult(ctx, result); err != nil {
		s.t.Fatalf("send result: %v", err)
	}
}

// Receive returns the next message from the server.
func (s *AgentSession) Receive(ctx context.Context) *wire.Envelope {
	s.t.Helper()
	msg, err := s.conn.ReceiveMessage(ctx)
	if err != nil {
		s.t.Fatalf("receive: %v", err)
	}
	return msg
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
