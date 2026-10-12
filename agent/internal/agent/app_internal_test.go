package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
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

// scriptedConn answers each ready with the next scripted message and records
// what the agent sends. It ends the conversation when the script runs out.
type scriptedConn struct {
	AgentConnection
	script []wire.Envelope
	sent   []string
}

func (c *scriptedConn) SendReady(context.Context) error {
	c.sent = append(c.sent, "ready")
	return nil
}

func (c *scriptedConn) SendResult(_ context.Context, r *wire.ResultPayload) error {
	c.sent = append(c.sent, "result:"+r.CommandID.String())
	return nil
}

func (c *scriptedConn) ReceiveMessage(context.Context) (*wire.Envelope, error) {
	if len(c.script) == 0 {
		return nil, errors.New("end of script")
	}
	msg := c.script[0]
	c.script = c.script[1:]
	return &msg, nil
}

type succeedingExecutor struct{}

func (succeedingExecutor) Execute(_ context.Context, cmd *wire.CommandPayload, _ func(wire.LogPayload)) (*wire.ResultPayload, error) {
	return &wire.ResultPayload{CommandID: cmd.ID, Success: true}, nil
}

func TestServe_AsksAgainAfterIdleAndAfterEachResult(t *testing.T) {
	id := uuid.New()
	data, _ := json.Marshal(wire.CommandPayload{ID: id, Type: wire.CommandHelmUpgrade})
	conn := &scriptedConn{script: []wire.Envelope{
		{Type: "idle"},
		{Type: "command", Data: data},
	}}

	err := serve(context.Background(), conn, succeedingExecutor{}, time.Millisecond, slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), "end of script") {
		t.Fatalf("expected the loop to end with the script, got %v", err)
	}
	want := []string{"ready", "ready", "result:" + id.String(), "ready"}
	if strings.Join(conn.sent, " ") != strings.Join(want, " ") {
		t.Errorf("expected %v, got %v", want, conn.sent)
	}
}
