package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/server/internal/events"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/service"
	"github.com/pondplatform/pond/server/internal/store"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// AgentHandler bridges agent WebSocket connections to the event bus. It owns
// no deployment state: every interaction with the deployment service goes
// through a published event, and every DB mutation happens on the service
// side in response to that event.
type AgentHandler struct {
	clusters  store.ClusterRepository
	agentConn service.AgentConnectionService
	log       *slog.Logger
}

func NewAgentHandler(clusters store.ClusterRepository, agentConn service.AgentConnectionService, log *slog.Logger) *AgentHandler {
	return &AgentHandler{
		clusters:  clusters,
		agentConn: agentConn,
		log:       log,
	}
}

func (h *AgentHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	token := auth.BearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing authorization")
		return
	}

	hash := auth.SHA256Hex(token)
	cluster, err := h.clusters.GetByTokenHash(r.Context(), hash)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Error("websocket upgrade failed", "cluster_id", cluster.ID, "err", err)
		return
	}
	defer conn.Close()

	log := h.log.With("cluster_id", cluster.ID)
	log.Info("agent connected")
	_ = h.clusters.UpdateLastSeen(r.Context(), cluster.ID, time.Now())

	ctx := r.Context()

	// Create session for event protocol handling
	session := h.agentConn.NewSession(cluster.ID, log)
	dispatchCh, wakeCh, err := session.Start(ctx)
	if err != nil {
		log.Error("start session failed", "err", err)
		return
	}

	// Connection state — only mutated on the main goroutine below.
	var activeCommandID uuid.UUID

	// On disconnect, publish AgentDisconnected so the service can requeue
	// whatever was in flight.
	defer func() {
		session.Close(activeCommandID)
	}()

	// Per-connection channels for WebSocket reader goroutine.
	wsCh := make(chan wire.Envelope, 4)
	readerDone := make(chan error, 1)

	// Reader goroutine: gorilla WS requires all ReadJSON calls from one
	// goroutine, so we park it here and push parsed envelopes onto wsCh.
	go func() {
		for {
			var env wire.Envelope
			if err := conn.ReadJSON(&env); err != nil {
				readerDone <- err
				close(wsCh)
				return
			}
			select {
			case wsCh <- env:
			case <-ctx.Done():
				return
			}
		}
	}()

	// sendCommand maps a domain.Command to wire.CommandPayload and writes it to the WebSocket.
	sendCommand := func(cmd *domain.Command) {
		log.Info("sending command to agent", "command_id", cmd.ID, "type", cmd.Type)
		wireCmd := wire.CommandPayload{
			ID:           cmd.ID,
			DeploymentID: cmd.DeploymentID,
			Type:         cmd.Type,
			Payload:      cmd.Payload,
			CreatedAt:    cmd.CreatedAt,
		}
		data, err := json.Marshal(wireCmd)
		if err != nil {
			log.Error("marshal command", "err", err)
			return
		}
		if err := conn.WriteJSON(wire.Envelope{Type: "command", Data: data}); err != nil {
			log.Error("write command to ws", "err", err)
		}
	}

	// requestNext asks the service for next command and sends it or idle.
	requestNext := func() {
		if cmd := session.RequestNext(ctx); cmd != nil {
			activeCommandID = cmd.ID
			sendCommand(cmd)
		} else {
			if err := conn.WriteJSON(wire.Envelope{Type: "idle"}); err != nil {
				log.Error("write idle to ws", "err", err)
			}
		}
	}

	// Main loop: single goroutine owns all connection state.
	for {
		select {
		case <-ctx.Done():
			return

		case err := <-readerDone:
			if err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Error("websocket read error", "err", err)
			}
			return

		case env, ok := <-wsCh:
			if !ok {
				return
			}
			switch env.Type {
			case "ready":
				requestNext()

			case "ack":
				var ack wire.AckPayload
				if err := json.Unmarshal(env.Data, &ack); err != nil {
					log.Error("decode ack", "err", err)
					continue
				}
				log.Info("command acknowledged by agent", "command_id", ack.CommandID)
				session.OnAck(ctx, ack.DeploymentID)

			case "result":
				var res events.CommandResult
				if err := json.Unmarshal(env.Data, &res); err != nil {
					log.Error("decode result", "err", err)
					continue
				}
				if activeCommandID == uuid.Nil {
					log.Warn("received result with no active command")
					continue
				}
				log.Info("received command result", "command_id", activeCommandID, "success", res.Success)
				res.CommandID = activeCommandID
				activeCommandID = uuid.Nil
				session.OnResult(ctx, res)
				requestNext()

			case "log":
				var msg wire.LogPayload
				if err := json.Unmarshal(env.Data, &msg); err != nil {
					log.Error("decode log", "err", err)
					continue
				}
				if activeCommandID == uuid.Nil {
					continue
				}
				session.OnLog(ctx, activeCommandID, msg.Line)

			default:
				log.Warn("unknown message type from agent", "type", env.Type)
			}

		case cmd := <-dispatchCh:
			// An unsolicited dispatch arrived outside of requestNext.
			if activeCommandID != uuid.Nil {
				log.Warn("dispatch arrived while busy, dropping", "command_id", cmd.ID)
				continue
			}
			activeCommandID = cmd.ID
			log.Info("sending unsolicited command to agent", "command_id", cmd.ID, "type", cmd.Type)
			sendCommand(cmd)

		case <-wakeCh:
			// A new command was enqueued for this cluster. If we're idle,
			// ask for it; otherwise the post-result requestNext will pick
			// it up when the current command finishes.
			if activeCommandID == uuid.Nil {
				requestNext()
			}
		}
	}
}
