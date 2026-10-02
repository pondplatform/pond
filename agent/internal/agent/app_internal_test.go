package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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
