package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pondplatform/pond/server/internal/auth"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/service"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// AgentHandler serves agent WebSocket connections, one per cluster. The agent
// asks for work with "ready" and reports with "result" and "log"; the server
// answers, and pushes "cancel" when the command it handed out should stop.
type AgentHandler struct {
	clusters store.ClusterRepository
	agents   *service.AgentCommands
	// stopCheck is how often a connection checks whether the command its
	// agent runs should stop.
	stopCheck time.Duration
	log       *slog.Logger
}

func NewAgentHandler(clusters store.ClusterRepository, agents *service.AgentCommands, stopCheck time.Duration, log *slog.Logger) *AgentHandler {
	return &AgentHandler{
		clusters:  clusters,
		agents:    agents,
		stopCheck: stopCheck,
		log:       log,
	}
}

func (h *AgentHandler) ServeWS(c *gin.Context) {
	r := c.Request

	token := auth.BearerToken(r)
	if token == "" {
		writeError(c, http.StatusUnauthorized, `missing agent token: send an "Authorization: Bearer <cluster agent token>" header`)
		return
	}

	cluster, err := h.clusters.GetByTokenHash(r.Context(), auth.SHA256Hex(token))
	if errors.Is(err, api.ErrNotFound) {
		writeError(c, http.StatusUnauthorized, "unknown agent token: no cluster uses it. It may have been rotated; update the agent with the cluster's current token")
		return
	}
	if err != nil {
		writeInternalError(c, fmt.Errorf("look up cluster by agent token: %w", err), h.log)
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, r, nil)
	if err != nil {
		h.log.Error("websocket upgrade failed", "cluster_id", cluster.ID, "err", err)
		return
	}
	defer conn.Close()

	ctx := r.Context()
	log := h.log.With("cluster_id", cluster.ID)
	log.Info("agent connected")
	_ = h.clusters.UpdateLastSeen(ctx, cluster.ID, time.Now())
	// Closing the connection makes the agent restart and connect again.
	if err := h.agents.Connected(ctx, cluster.ID); err != nil {
		log.Error("fail the commands dispatched before the agent connected", "err", err)
		return
	}
	if err := h.serve(ctx, conn, cluster.ID, log); err != nil {
		log.Warn("closing agent connection", "err", err)
	}
	log.Info("agent disconnected")
}

// serve answers the agent until the connection ends. It remembers the
// command it handed out, and while the agent runs it, checks every stopCheck
// whether the command's deployment has ended; if so it pushes "cancel" once.
// When a result cannot be stored it returns an error, which closes the
// connection: the command stays dispatched until the agent connects again.
func (h *AgentHandler) serve(ctx context.Context, conn *websocket.Conn, clusterID uuid.UUID, log *slog.Logger) error {
	inbox := make(chan wire.Envelope)
	readErr := make(chan error, 1)
	done := make(chan struct{})
	defer close(done)
	// gorilla/websocket allows one reader at a time, so reading gets its own
	// goroutine; every write stays on this one.
	go func() {
		for {
			var env wire.Envelope
			if err := conn.ReadJSON(&env); err != nil {
				readErr <- err
				return
			}
			select {
			case inbox <- env:
			case <-done:
				return
			}
		}
	}()

	var running *uuid.UUID
	cancelSent := false
	ticker := time.NewTicker(h.stopCheck)
	defer ticker.Stop()

	for {
		select {
		case err := <-readErr:
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("read: %w", err)

		case <-ticker.C:
			if running == nil || cancelSent {
				continue
			}
			stop, err := h.agents.ShouldStop(ctx, *running)
			if err != nil {
				log.Error("check whether to stop command", "command_id", *running, "err", err)
				continue
			}
			if stop {
				log.Info("telling agent to cancel command", "command_id", *running)
				if err := send(conn, wire.MessageCancel, wire.CancelPayload{CommandID: *running}); err != nil {
					return err
				}
				cancelSent = true
			}

		case env := <-inbox:
			switch env.Type {
			case wire.MessageReady:
				cmd, err := h.agents.Next(ctx, clusterID)
				if err != nil {
					log.Error("claim command", "err", err)
				}
				if cmd != nil {
					running, cancelSent = &cmd.ID, false
				}
				if err := reply(conn, cmd, log); err != nil {
					return err
				}

			case wire.MessageResult:
				var res wire.ResultPayload
				if err := json.Unmarshal(env.Data, &res); err != nil {
					log.Error("decode result", "err", err)
					continue
				}
				log.Info("received command result", "command_id", res.CommandID, "success", res.Success)
				if err := h.agents.Record(ctx, clusterID, service.CommandResult{
					CommandID: res.CommandID,
					Success:   res.Success,
					Output:    res.Output,
					Error:     res.Error,
				}); err != nil {
					return err
				}
				if running != nil && *running == res.CommandID {
					running = nil
				}

			case wire.MessageLog:
				var msg wire.LogPayload
				if err := json.Unmarshal(env.Data, &msg); err != nil {
					log.Error("decode log", "err", err)
					continue
				}
				if msg.CommandID == uuid.Nil {
					continue
				}
				if err := h.agents.Log(ctx, clusterID, msg.CommandID, msg.Line); err != nil {
					log.Error("store command log", "command_id", msg.CommandID, "err", err)
				}

			default:
				log.Warn("unknown message type from agent", "type", env.Type)
			}
		}
	}
}

func send(conn *websocket.Conn, msgType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", msgType, err)
	}
	if err := conn.WriteJSON(wire.Envelope{Type: msgType, Data: data}); err != nil {
		return fmt.Errorf("send %s: %w", msgType, err)
	}
	return nil
}

// reply answers a "ready": the command to run, or "idle" when there is none.
func reply(conn *websocket.Conn, cmd *domain.Command, log *slog.Logger) error {
	if cmd == nil {
		return conn.WriteJSON(wire.Envelope{Type: wire.MessageIdle})
	}
	log.Info("sending command to agent", "command_id", cmd.ID, "type", cmd.Type)
	return send(conn, wire.MessageCommand, wire.CommandPayload{
		ID:           cmd.ID,
		DeploymentID: cmd.DeploymentID,
		Type:         cmd.Type,
		Payload:      cmd.Payload,
		CreatedAt:    cmd.CreatedAt,
	})
}
