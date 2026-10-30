package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/agent/wire"
)

func TestUndecodableResult_FailsTheCommand(t *testing.T) {
	id, depID := uuid.New(), uuid.New()
	data := json.RawMessage(`{"id":"` + id.String() + `","deployment_id":"` + depID.String() + `","type":"helm.upgrade","payload":"not-an-object","created_at":"yesterday"}`)

	r := undecodableResult(data, errors.New("bad created_at"))
	if r == nil {
		t.Fatal("expected a result")
	}
	if r.CommandID != id || r.DeploymentID != depID || r.Success {
		t.Errorf("unexpected result: %+v", r)
	}
	if !strings.Contains(r.Error, "could not decode the command (bad created_at)") {
		t.Errorf("unexpected error text: %q", r.Error)
	}
}

func TestUndecodableResult_WithoutIDIsNil(t *testing.T) {
	if r := undecodableResult(json.RawMessage(`"garbage"`), errors.New("x")); r != nil {
		t.Errorf("expected no result, got %+v", r)
	}
}

// scriptedConn plays the server over a series of connections. Each
// connection has a script: the replies to its readies, in order. A ready
// past the end of the script drops the connection, as does drop. Once the
// scripts run out, the next Connect calls stop, which ends the test's run.
// It records what the agent sends, across connections.
type scriptedConn struct {
	stop context.CancelFunc

	mu           sync.Mutex
	scripts      [][]wire.Envelope
	failConnects int
	connects     int
	replies      []wire.Envelope
	out          chan *wire.Envelope
	open         bool
	sent         []string
}

func newScriptedConn(stop context.CancelFunc, scripts ...[]wire.Envelope) *scriptedConn {
	return &scriptedConn{stop: stop, scripts: scripts}
}

func (c *scriptedConn) Connect(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connects++
	if c.failConnects > 0 {
		c.failConnects--
		return errors.New("connection refused")
	}
	if len(c.scripts) == 0 {
		c.stop()
		return errors.New("end of script")
	}
	c.replies, c.scripts = c.scripts[0], c.scripts[1:]
	c.out = make(chan *wire.Envelope, 16)
	c.open = true
	return nil
}

// drop ends the current connection, as a server going away does.
func (c *scriptedConn) drop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropLocked()
}

func (c *scriptedConn) dropLocked() {
	if c.open {
		c.open = false
		close(c.out)
	}
}

func (c *scriptedConn) Close() error {
	c.drop()
	return nil
}

func (c *scriptedConn) record(s string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.open {
		return errors.New("not connected")
	}
	c.sent = append(c.sent, s)
	return nil
}

func (c *scriptedConn) Sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent...)
}

func (c *scriptedConn) SendHello(_ context.Context, h wire.HelloPayload) error {
	return c.record(helloEvent(h.Restarted, h.Running, h.Finished...))
}

func (c *scriptedConn) SendReady(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.open {
		return errors.New("not connected")
	}
	c.sent = append(c.sent, "ready")
	if len(c.replies) == 0 {
		c.dropLocked()
		return nil
	}
	reply := c.replies[0]
	c.replies = c.replies[1:]
	c.out <- &reply
	return nil
}

func (c *scriptedConn) SendResult(_ context.Context, r *wire.ResultPayload) error {
	return c.record(resultEvent(r.CommandID, r.Error))
}

func (c *scriptedConn) SendLog(_ context.Context, entry wire.LogPayload) error {
	return c.record("log " + entry.Line)
}

func (c *scriptedConn) ReceiveMessage(ctx context.Context) (*wire.Envelope, error) {
	c.mu.Lock()
	out := c.out
	c.mu.Unlock()
	select {
	case msg, ok := <-out:
		if !ok {
			return nil, errors.New("connection dropped")
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// push sends a message unprompted, as the server does for cancel.
func (c *scriptedConn) push(msgType string, payload any) {
	data, _ := json.Marshal(payload)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.out <- &wire.Envelope{Type: msgType, Data: data}
}

func helloEvent(restarted bool, running *uuid.UUID, finished ...uuid.UUID) string {
	s := fmt.Sprintf("hello restarted=%t", restarted)
	if running != nil {
		s += " running=" + running.String()
	}
	for _, id := range finished {
		s += " finished=" + id.String()
	}
	return s
}

func resultEvent(id uuid.UUID, errText string) string {
	return strings.TrimSpace("result " + id.String() + " " + errText)
}

func commandMessage(id uuid.UUID) wire.Envelope {
	data, _ := json.Marshal(wire.CommandPayload{ID: id, Type: wire.CommandHelmUpgrade})
	return wire.Envelope{Type: wire.MessageCommand, Data: data}
}

var idle = wire.Envelope{Type: wire.MessageIdle}

type succeedingExecutor struct{}

func (succeedingExecutor) Execute(_ context.Context, cmd *wire.CommandPayload, _ func(wire.LogPayload)) (*wire.ResultPayload, error) {
	return &wire.ResultPayload{CommandID: cmd.ID, Success: true}, nil
}

// gatedExecutor runs until release is closed, then succeeds, or until its
// context is cancelled, then reports why.
type gatedExecutor struct{ started, release chan struct{} }

func newGatedExecutor() gatedExecutor {
	return gatedExecutor{started: make(chan struct{}), release: make(chan struct{})}
}

func (e gatedExecutor) Execute(ctx context.Context, cmd *wire.CommandPayload, _ func(wire.LogPayload)) (*wire.ResultPayload, error) {
	close(e.started)
	select {
	case <-e.release:
		return &wire.ResultPayload{CommandID: cmd.ID, Success: true}, nil
	case <-ctx.Done():
		return &wire.ResultPayload{CommandID: cmd.ID, Success: false, Error: context.Cause(ctx).Error()}, nil
	}
}

// runScripted runs a worker against conn until its scripts run out, and
// fails the test if that takes too long.
func runScripted(t *testing.T, ctx context.Context, conn *scriptedConn, exec CommandExecutor) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		newWorker(conn, exec, time.Millisecond, slog.New(slog.DiscardHandler)).run(ctx, time.Millisecond, time.Millisecond)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("the agent did not finish its script; sent %v", conn.Sent())
	}
}

// sentEventually waits until the agent has sent want, and reports whether it
// did in time.
func sentEventually(conn *scriptedConn, want string) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range conn.Sent() {
			if s == want {
				return true
			}
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func expectSent(t *testing.T, conn *scriptedConn, want ...string) {
	t.Helper()
	if got := conn.Sent(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("expected\n  %s\ngot\n  %s", strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	}
}

func TestServe_AsksAgainAfterIdleAndAfterEachResult(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	id := uuid.New()
	conn := newScriptedConn(stop, []wire.Envelope{idle, commandMessage(id)})

	runScripted(t, ctx, conn, succeedingExecutor{})
	expectSent(t, conn, helloEvent(true, nil), "ready", "ready", resultEvent(id, ""), "ready")
}

func TestServe_CancelStopsTheRunningCommand(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	id := uuid.New()
	conn := newScriptedConn(stop, []wire.Envelope{commandMessage(id), idle})
	exec := newGatedExecutor()

	go func() {
		<-exec.started
		conn.push(wire.MessageCancel, wire.CancelPayload{CommandID: id})
	}()
	runScripted(t, ctx, conn, exec)
	expectSent(t, conn, helloEvent(true, nil), "ready", resultEvent(id, errCancelledByServer.Error()), "ready", "ready")
}

func TestRun_SaysItRestartedUntilTheServerAnswers(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	// The first connection drops before the server answers its ready.
	conn := newScriptedConn(stop, nil, []wire.Envelope{idle}, nil)

	runScripted(t, ctx, conn, succeedingExecutor{})
	expectSent(t, conn,
		helloEvent(true, nil), "ready",
		helloEvent(true, nil), "ready", "ready",
		helloEvent(false, nil), "ready")
}

func TestRun_KeepsRunningTheCommandAcrossADrop(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	id := uuid.New()
	conn := newScriptedConn(stop, []wire.Envelope{commandMessage(id)}, []wire.Envelope{idle})
	exec := newGatedExecutor()

	go func() {
		<-exec.started
		conn.drop()
		if !sentEventually(conn, helloEvent(false, &id)) {
			t.Errorf("expected the hello after the drop to name the running command, got %v", conn.Sent())
		}
		close(exec.release)
	}()
	runScripted(t, ctx, conn, exec)
	expectSent(t, conn,
		helloEvent(true, nil), "ready",
		helloEvent(false, &id), resultEvent(id, ""), "ready", "ready")
}

func TestRun_SendsAgainAResultTheServerDidNotConfirm(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	id := uuid.New()
	// The connection drops after the result, before the server answers the
	// ready that follows it.
	conn := newScriptedConn(stop, []wire.Envelope{commandMessage(id)}, []wire.Envelope{idle})

	runScripted(t, ctx, conn, succeedingExecutor{})
	expectSent(t, conn,
		helloEvent(true, nil), "ready", resultEvent(id, ""), "ready",
		helloEvent(false, nil, id), resultEvent(id, ""), "ready", "ready")
}

func TestRun_RetriesConnecting(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	conn := newScriptedConn(stop, []wire.Envelope{idle})
	conn.failConnects = 2

	runScripted(t, ctx, conn, succeedingExecutor{})
	if conn.connects != 4 {
		t.Errorf("expected 2 failed attempts, then a connection, then the end of the script; got %d attempts", conn.connects)
	}
	expectSent(t, conn, helloEvent(true, nil), "ready", "ready")
}

func TestHello_SendsAResultThatFinishedWhileDisconnected(t *testing.T) {
	id := uuid.New()
	conn := newScriptedConn(func() {}, nil)
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := newWorker(conn, succeedingExecutor{}, time.Millisecond, slog.New(slog.DiscardHandler))
	w.restarted = false
	w.running = &runningCommand{id: id, cancel: func(error) {}, result: make(chan *wire.ResultPayload, 1)}
	w.running.result <- &wire.ResultPayload{CommandID: id, Success: true}
	w.logs.send(context.Background(), wire.LogPayload{CommandID: id, Line: "done"})

	if err := w.hello(context.Background()); err != nil {
		t.Fatal(err)
	}
	expectSent(t, conn, helloEvent(false, nil, id), "log done", resultEvent(id, ""), "ready")
}

// logConn records the log lines it is sent, and fails them while down.
type logConn struct {
	AgentConnection
	down  bool
	lines []string
}

func (c *logConn) SendLog(_ context.Context, entry wire.LogPayload) error {
	if c.down {
		return errors.New("not connected")
	}
	c.lines = append(c.lines, entry.Line)
	return nil
}

func TestLogRelay_KeepsLinesUntilResumed(t *testing.T) {
	ctx := context.Background()
	conn := &logConn{}
	r := &logRelay{conn: conn}
	line := func(s string) wire.LogPayload { return wire.LogPayload{Line: s} }

	r.send(ctx, line("a"))
	if len(conn.lines) != 0 {
		t.Fatalf("expected nothing sent before resume, got %v", conn.lines)
	}
	if err := r.resume(ctx); err != nil {
		t.Fatal(err)
	}
	r.send(ctx, line("b"))
	conn.down = true
	r.send(ctx, line("c"))
	conn.down = false
	r.send(ctx, line("d"))
	if err := r.resume(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(conn.lines, " "); got != "a b c d" {
		t.Errorf("expected the lines in order, a failed send kept until resume; got %q", got)
	}
}

func TestLogRelay_DropsTheOldestOverTheLimit(t *testing.T) {
	ctx := context.Background()
	conn := &logConn{}
	r := &logRelay{conn: conn}

	for i := range logRelayLimit + 2 {
		r.send(ctx, wire.LogPayload{Line: fmt.Sprint(i)})
	}
	if err := r.resume(ctx); err != nil {
		t.Fatal(err)
	}
	if len(conn.lines) != logRelayLimit+1 {
		t.Fatalf("expected a notice and %d lines, got %d lines", logRelayLimit, len(conn.lines))
	}
	if want := "2 log lines were dropped while the agent was disconnected from the server"; conn.lines[0] != want {
		t.Errorf("expected the notice %q first, got %q", want, conn.lines[0])
	}
	if conn.lines[1] != "2" || conn.lines[logRelayLimit] != fmt.Sprint(logRelayLimit+1) {
		t.Errorf("expected lines 2 to %d, got %q to %q", logRelayLimit+1, conn.lines[1], conn.lines[logRelayLimit])
	}
}
