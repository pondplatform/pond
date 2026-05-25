package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pondplatform/pond/shared/agent/wire"
)

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
func (c *connection) ReceiveMessage(ctx context.Context) (*wire.Envelope, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	type result struct {
		env *wire.Envelope
		err error
	}
	ch := make(chan result, 1)
	go func() {
		var env wire.Envelope
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

func (c *connection) SendAck(ctx context.Context, cmd *wire.CommandPayload) error {
	return c.send("ack", wire.AckPayload{
		CommandID: cmd.ID,
	})
}

func (c *connection) SendResult(ctx context.Context, result *wire.ResultPayload) error {
	return c.send("result", result)
}

func (c *connection) SendLog(ctx context.Context, entry wire.LogPayload) error {
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
	err = conn.WriteJSON(wire.Envelope{Type: msgType, Data: data})
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
