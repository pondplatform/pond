package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/agent/wire"
)

type recordingConn struct {
	AgentConnection
	results []*wire.ResultPayload
}

func (c *recordingConn) SendResult(_ context.Context, r *wire.ResultPayload) error {
	c.results = append(c.results, r)
	return nil
}

func TestReportUndecodableCommand_SendsFailedResult(t *testing.T) {
	id, depID := uuid.New(), uuid.New()
	data := json.RawMessage(`{"id":"` + id.String() + `","deployment_id":"` + depID.String() + `","type":"helm.upgrade","payload":"not-an-object","created_at":"yesterday"}`)
	conn := &recordingConn{}

	if err := reportUndecodableCommand(context.Background(), conn, data, errors.New("bad created_at")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conn.results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(conn.results))
	}
	r := conn.results[0]
	if r.CommandID != id || r.DeploymentID != depID || r.Success {
		t.Errorf("unexpected result: %+v", r)
	}
	if !strings.Contains(r.Error, "could not decode the command (bad created_at)") {
		t.Errorf("unexpected error text: %q", r.Error)
	}
}

func TestReportUndecodableCommand_WithoutIDSendsNothing(t *testing.T) {
	conn := &recordingConn{}
	if err := reportUndecodableCommand(context.Background(), conn, json.RawMessage(`"garbage"`), errors.New("x")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conn.results) != 0 {
		t.Errorf("expected no result, got %d", len(conn.results))
	}
}

// scriptedConn plays the server: it answers each ready with the next scripted
// reply, ends the conversation once the script runs out, and records what the
// agent sends. push sends a message unprompted, as the server does for cancel.
type scriptedConn struct {
	AgentConnection
	replies []wire.Envelope
	out     chan *wire.Envelope
	mu      sync.Mutex
	sent    []string
}

func newScriptedConn(replies ...wire.Envelope) *scriptedConn {
	return &scriptedConn{replies: replies, out: make(chan *wire.Envelope, 16)}
}

func (c *scriptedConn) record(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, s)
}

func (c *scriptedConn) Sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent...)
}

func (c *scriptedConn) SendReady(context.Context) error {
	c.record("ready")
	if len(c.replies) == 0 {
		close(c.out)
		return nil
	}
	reply := c.replies[0]
	c.replies = c.replies[1:]
	c.out <- &reply
	return nil
}

func (c *scriptedConn) SendResult(_ context.Context, r *wire.ResultPayload) error {
	c.record("result:" + r.Error)
	return nil
}

func (c *scriptedConn) SendLog(context.Context, wire.LogPayload) error { return nil }

func (c *scriptedConn) ReceiveMessage(ctx context.Context) (*wire.Envelope, error) {
	select {
	case msg, ok := <-c.out:
		if !ok {
			return nil, errors.New("end of script")
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *scriptedConn) push(msgType string, payload any) {
	data, _ := json.Marshal(payload)
	c.out <- &wire.Envelope{Type: msgType, Data: data}
}

func commandMessage(id uuid.UUID) wire.Envelope {
	data, _ := json.Marshal(wire.CommandPayload{ID: id, Type: wire.CommandHelmUpgrade})
	return wire.Envelope{Type: wire.MessageCommand, Data: data}
}

type succeedingExecutor struct{}

func (succeedingExecutor) Execute(_ context.Context, cmd *wire.CommandPayload, _ func(wire.LogPayload)) (*wire.ResultPayload, error) {
	return &wire.ResultPayload{CommandID: cmd.ID, Success: true}, nil
}

// blockingExecutor runs until its context is cancelled, then reports why.
type blockingExecutor struct{ started chan struct{} }

func (e blockingExecutor) Execute(ctx context.Context, cmd *wire.CommandPayload, _ func(wire.LogPayload)) (*wire.ResultPayload, error) {
	close(e.started)
	<-ctx.Done()
	return &wire.ResultPayload{CommandID: cmd.ID, Success: false, Error: context.Cause(ctx).Error()}, nil
}

func TestServe_AsksAgainAfterIdleAndAfterEachResult(t *testing.T) {
	conn := newScriptedConn(wire.Envelope{Type: wire.MessageIdle}, commandMessage(uuid.New()))

	err := serve(context.Background(), conn, succeedingExecutor{}, time.Millisecond, slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), "end of script") {
		t.Fatalf("expected the loop to end with the script, got %v", err)
	}
	want := []string{"ready", "ready", "result:", "ready"}
	if got := conn.Sent(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestServe_CancelStopsTheRunningCommand(t *testing.T) {
	id := uuid.New()
	conn := newScriptedConn(commandMessage(id))
	exec := blockingExecutor{started: make(chan struct{})}

	go func() {
		<-exec.started
		conn.push(wire.MessageCancel, wire.CancelPayload{CommandID: id})
	}()
	err := serve(context.Background(), conn, exec, time.Millisecond, slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), "end of script") {
		t.Fatalf("expected the loop to end with the script, got %v", err)
	}
	want := []string{"ready", "result:" + errCancelledByServer.Error(), "ready"}
	if got := conn.Sent(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("expected %v, got %v", want, got)
	}
}
