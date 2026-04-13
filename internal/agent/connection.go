package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// Envelope is the wire format for all WebSocket messages between agent and server.
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// wsEnvelope is an alias used internally for JSON marshalling.
type wsEnvelope = Envelope

type connection struct {
	serverAddr string
	token      string
	mu         sync.Mutex
	conn       *websocket.Conn
}

func NewConnection(serverAddr, token string) AgentConnection {
	return &connection{
		serverAddr: serverAddr,
		token:      token,
	}
}

func (c *connection) Connect(ctx context.Context) error {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.token)

	url := "ws://" + c.serverAddr + "/agent/ws"
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, header)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	return nil
}

// ReceiveMessage reads the next envelope from the server.
// It blocks until a message arrives or ctx is cancelled.
func (c *connection) ReceiveMessage(ctx context.Context) (*wsEnvelope, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	type result struct {
		env *wsEnvelope
		err error
	}
	ch := make(chan result, 1)
	go func() {
		var env wsEnvelope
		if err := conn.ReadJSON(&env); err != nil {
			ch <- result{err: fmt.Errorf("read websocket: %w", err)}
			return
		}
		ch <- result{env: &env}
	}()

	select {
	case <-ctx.Done():
		conn.Close()
		return nil, ctx.Err()
	case r := <-ch:
		return r.env, r.err
	}
}

func (c *connection) SendReady(ctx context.Context) error {
	return c.send("ready", struct{}{})
}

func (c *connection) SendAck(ctx context.Context, cmd *Command) error {
	return c.send("ack", struct {
		CommandID    any `json:"command_id"`
		DeploymentID any `json:"deployment_id"`
	}{
		CommandID:    cmd.ID,
		DeploymentID: cmd.DeploymentID,
	})
}

func (c *connection) SendResult(ctx context.Context, result *CommandResult) error {
	return c.send("result", result)
}

func (c *connection) SendLog(ctx context.Context, entry LogEntry) error {
	return c.send("log", entry)
}

func (c *connection) send(msgType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	c.mu.Lock()
	conn := c.conn
	if conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	err = conn.WriteJSON(wsEnvelope{Type: msgType, Data: data})
	c.mu.Unlock()
	return err
}

func (c *connection) Close() error {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn == nil {
		return nil
	}
	return conn.Close()
}
