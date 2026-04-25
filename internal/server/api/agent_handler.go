package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/auth"
	"github.com/pondplatform/pond/internal/server/events"
	"github.com/pondplatform/pond/internal/server/service"
	"github.com/pondplatform/pond/internal/server/store"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsEnvelope is the wire format for all WebSocket messages.
type wsEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// wsAck is the payload of an "ack" message from the agent.
type wsAck struct {
	CommandID    uuid.UUID `json:"commandId"`
	DeploymentID uuid.UUID `json:"deploymentId"`
}

// wsLog is the payload of a "log" message from the agent.
type wsLog struct {
	Line string `json:"line"`
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
	wsCh := make(chan wsEnvelope, 4)
	readerDone := make(chan error, 1)

	// Reader goroutine: gorilla WS requires all ReadJSON calls from one
	// goroutine, so we park it here and push parsed envelopes onto wsCh.
	go func() {
		for {
			var env wsEnvelope
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

	// sendCommand marshals and writes a command to the WebSocket.
	sendCommand := func(cmd *domain.Command) {
		log.Info("sending command to agent", "command_id", cmd.ID, "type", cmd.Type)
		data, err := json.Marshal(cmd)
		if err != nil {
			log.Error("marshal command", "err", err)
			return
		}
		if err := conn.WriteJSON(wsEnvelope{Type: "command", Data: data}); err != nil {
			log.Error("write command to ws", "err", err)
		}
	}

	// requestNext asks the service for next command and sends it or idle.
	requestNext := func() {
		if cmd := session.RequestNext(ctx); cmd != nil {
			activeCommandID = cmd.ID
			sendCommand(cmd)
		} else {
			if err := conn.WriteJSON(wsEnvelope{Type: "idle"}); err != nil {
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
				var ack wsAck
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
				var msg wsLog
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

