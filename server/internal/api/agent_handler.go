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

// AgentHandler serves agent WebSocket connections. The agent drives the
// conversation: it asks for work with "ready" and reports with "result" and
// "log"; the server only ever answers.
type AgentHandler struct {
	clusters store.ClusterRepository
	agents   *service.AgentCommands
	log      *slog.Logger
}

func NewAgentHandler(clusters store.ClusterRepository, agents *service.AgentCommands, log *slog.Logger) *AgentHandler {
	return &AgentHandler{
		clusters: clusters,
		agents:   agents,
		log:      log,
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
	if err := h.serve(ctx, conn, cluster.ID, log); err != nil {
		log.Warn("closing agent connection", "err", err)
	}
	log.Info("agent disconnected")
}

// serve answers the agent's messages until the connection ends. When a
// result cannot be stored it returns an error, which closes the connection:
// the agent cannot tell, and the command stays dispatched.
func (h *AgentHandler) serve(ctx context.Context, conn *websocket.Conn, clusterID uuid.UUID, log *slog.Logger) error {
	for {
		var env wire.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}

		switch env.Type {
		case "ready":
			cmd, err := h.agents.Next(ctx, clusterID)
			if err != nil {
				log.Error("claim command", "err", err)
			}
			if err := reply(conn, cmd, log); err != nil {
				return err
			}

		case "result":
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

		case "log":
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

// reply answers a "ready": the command to run, or "idle" when there is none.
func reply(conn *websocket.Conn, cmd *domain.Command, log *slog.Logger) error {
	if cmd == nil {
		return conn.WriteJSON(wire.Envelope{Type: "idle"})
	}
	log.Info("sending command to agent", "command_id", cmd.ID, "type", cmd.Type)
	data, err := json.Marshal(wire.CommandPayload{
		ID:           cmd.ID,
		DeploymentID: cmd.DeploymentID,
		Type:         cmd.Type,
		Payload:      cmd.Payload,
		CreatedAt:    cmd.CreatedAt,
	})
	if err != nil {
		return fmt.Errorf("marshal command %s: %w", cmd.ID, err)
	}
	if err := conn.WriteJSON(wire.Envelope{Type: "command", Data: data}); err != nil {
		return fmt.Errorf("send command %s: %w", cmd.ID, err)
	}
	return nil
}
