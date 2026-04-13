package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pondplatform/pond/internal/agent"
)

// wsEnvelope mirrors agent.Envelope for use in test server handlers.
type wsEnvelope = agent.Envelope

func newTestServer(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		handler(conn)
	}))
	return srv
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestConnection_Connect(t *testing.T) {
	srv := newTestServer(t, func(conn *websocket.Conn) {
		// Just accept and close cleanly.
	})
	defer srv.Close()

	addr := strings.TrimPrefix(wsURL(srv), "ws://")
	conn := agent.NewConnection(addr, "test-token")
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	conn.Close()
}

func TestConnection_ReceiveCommand(t *testing.T) {
	cmdID := uuid.New()
	deployID := uuid.New()

	srv := newTestServer(t, func(conn *websocket.Conn) {
		cmd := agent.Command{
			ID:           cmdID,
			DeploymentID: deployID,
			Type:         agent.CommandHelmUpgrade,
			Payload:      json.RawMessage(`{"releaseName":"test"}`),
			CreatedAt:    time.Now(),
		}
		data, _ := json.Marshal(cmd)
		_ = conn.WriteJSON(wsEnvelope{Type: "command", Data: data})
		// Wait for client to close.
		conn.ReadMessage()
	})
	defer srv.Close()

	addr := strings.TrimPrefix(wsURL(srv), "ws://")
	c := agent.NewConnection(addr, "tok")
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	env, err := c.ReceiveMessage(context.Background())
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if env.Type != "command" {
		t.Fatalf("expected type 'command', got %q", env.Type)
	}
	var cmd agent.Command
	if err := json.Unmarshal(env.Data, &cmd); err != nil {
		t.Fatalf("decode command: %v", err)
	}
	if cmd.ID != cmdID {
		t.Errorf("expected id %s, got %s", cmdID, cmd.ID)
	}
}

func TestConnection_SendResult(t *testing.T) {
	received := make(chan wsEnvelope, 1)

	srv := newTestServer(t, func(conn *websocket.Conn) {
		var env wsEnvelope
		if err := conn.ReadJSON(&env); err == nil {
			received <- env
		}
	})
	defer srv.Close()

	addr := strings.TrimPrefix(wsURL(srv), "ws://")
	c := agent.NewConnection(addr, "tok")
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	result := &agent.CommandResult{
		CommandID: uuid.New(),
		Success:   true,
	}
	if err := c.SendResult(context.Background(), result); err != nil {
		t.Fatalf("SendResult: %v", err)
	}

	select {
	case env := <-received:
		if env.Type != "result" {
			t.Errorf("expected type 'result', got %q", env.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server to receive result")
	}
}

func TestConnection_SendLog(t *testing.T) {
	received := make(chan wsEnvelope, 1)

	srv := newTestServer(t, func(conn *websocket.Conn) {
		var env wsEnvelope
		if err := conn.ReadJSON(&env); err == nil {
			received <- env
		}
	})
	defer srv.Close()

	addr := strings.TrimPrefix(wsURL(srv), "ws://")
	c := agent.NewConnection(addr, "tok")
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	entry := agent.LogEntry{
		CommandID: uuid.New(),
		Line:      "hello from agent",
		Timestamp: time.Now(),
		Stream:    "stdout",
	}
	if err := c.SendLog(context.Background(), entry); err != nil {
		t.Fatalf("SendLog: %v", err)
	}

	select {
	case env := <-received:
		if env.Type != "log" {
			t.Errorf("expected type 'log', got %q", env.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server to receive log")
	}
}
