package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// wsEnvelope is the wire format for all WebSocket messages.
type wsEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

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

func (c *connection) ReceiveCommand(ctx context.Context) (*Command, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Run blocking read in a goroutine so we can respect ctx cancellation.
	type result struct {
		cmd *Command
		err error
	}
	ch := make(chan result, 1)
	go func() {
		var env wsEnvelope
		if err := conn.ReadJSON(&env); err != nil {
			ch <- result{err: fmt.Errorf("read websocket: %w", err)}
			return
		}
		if env.Type != "command" {
			ch <- result{err: fmt.Errorf("unexpected message type: %s", env.Type)}
			return
		}
		var cmd Command
		if err := json.Unmarshal(env.Data, &cmd); err != nil {
			ch <- result{err: fmt.Errorf("decode command: %w", err)}
			return
		}
		ch <- result{cmd: &cmd}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.cmd, r.err
	}
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
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}

	c.mu.Lock()
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
