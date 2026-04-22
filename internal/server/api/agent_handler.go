package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/auth"
	"github.com/pondplatform/pond/internal/server/events"
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
	CommandID    uuid.UUID `json:"command_id"`
	DeploymentID uuid.UUID `json:"deployment_id"`
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
	clusters store.ClusterRepository
	bus      events.Bus
}

func NewAgentHandler(clusters store.ClusterRepository, bus events.Bus) *AgentHandler {
	return &AgentHandler{
		clusters: clusters,
		bus:      bus,
	}
}

func (h *AgentHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	token := auth.BearerToken(r)
	if token == "" {
		http.Error(w, "missing authorization", http.StatusUnauthorized)
		return
	}

	hash := auth.SHA256Hex(token)
	cluster, err := h.clusters.GetByTokenHash(r.Context(), hash)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("agent ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("agent connected: cluster=%s", cluster.ID)
	_ = h.clusters.UpdateLastSeen(r.Context(), cluster.ID, time.Now())

	ctx := r.Context()

	// Per-connection channels. All connection state lives on a single
	// goroutine (the main select loop below); subscribers and the reader
	// goroutine only communicate with it through these channels.
	wsCh := make(chan wsEnvelope, 4)
	dispatchCh := make(chan *domain.Command, 1)
	wakeCh := make(chan struct{}, 1)
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

	// Per-event-type cluster subscribers: the only bridge from the bus into
	// this connection's goroutine. Never touch handler-local state directly.
	unsubDispatch := h.bus.Subscribe(events.ClusterCommandDispatchTopic(cluster.ID), func(v any) {
		e, ok := v.(events.CommandDispatch)
		if !ok {
			return
		}
		select {
		case dispatchCh <- e.Cmd:
		default:
			// A dispatch is already queued for the main loop to
			// consume. The service only dispatches in response to
			// AgentReady (one outstanding credit), so this path
			// should be unreachable — log if we ever hit it.
			log.Printf("agent ws: dropped duplicate CommandDispatch for cluster %s", cluster.ID)
		}
	})
	defer unsubDispatch()

	unsubQueued := h.bus.Subscribe(events.ClusterCommandQueuedTopic(cluster.ID), func(v any) {
		if _, ok := v.(events.CommandQueued); !ok {
			return
		}
		select {
		case wakeCh <- struct{}{}:
		default:
		}
	})
	defer unsubQueued()

	// Connection state — only mutated on the main goroutine below.
	var activeCommandID uuid.UUID

	// requestNext publishes AgentReady and, because the in-memory bus
	// delivers synchronously, any CommandDispatch the service emits in
	// response will already be sitting on dispatchCh by the time Publish
	// returns. We pull it (non-blocking) and either send a command frame
	// or fall back to an "idle" frame.
	requestNext := func() {
		h.bus.Publish(ctx, events.TopicAgentReady, events.AgentReady{ClusterID: cluster.ID})
		select {
		case cmd := <-dispatchCh:
			activeCommandID = cmd.ID
			data, err := json.Marshal(cmd)
			if err != nil {
				log.Printf("marshal command: %v", err)
				return
			}
			if err := conn.WriteJSON(wsEnvelope{Type: "command", Data: data}); err != nil {
				log.Printf("agent ws write command: %v", err)
			}
		default:
			if err := conn.WriteJSON(wsEnvelope{Type: "idle"}); err != nil {
				log.Printf("agent ws write idle: %v", err)
			}
		}
	}

	// On disconnect, publish AgentDisconnected so the service can requeue
	// whatever was in flight.
	defer func() {
		h.bus.Publish(context.Background(), events.TopicAgentDisconnected, events.AgentDisconnected{
			ClusterID:         cluster.ID,
			InFlightCommandID: activeCommandID,
		})
	}()

	// Main loop: single goroutine owns all connection state. Reader
	// goroutine feeds wsCh; bus subscriber feeds dispatchCh/wakeCh.
	for {
		select {
		case <-ctx.Done():
			return

		case err := <-readerDone:
			if err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("agent ws read: %v", err)
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
					log.Printf("decode ack: %v", err)
					continue
				}
				h.bus.Publish(ctx, events.TopicCommandStarted, events.CommandStarted{
					DeploymentID: ack.DeploymentID,
				})

			case "result":
				var res events.CommandResult
				if err := json.Unmarshal(env.Data, &res); err != nil {
					log.Printf("decode result: %v", err)
					continue
				}
				if activeCommandID == uuid.Nil {
					log.Printf("received result with no active command")
					continue
				}
				// Trust the server-tracked command ID, not the agent-supplied one.
				res.CommandID = activeCommandID
				activeCommandID = uuid.Nil
				// Synchronous bus: by the time this returns, the service
				// has persisted the result and advanced the state machine.
				h.bus.Publish(ctx, events.TopicCommandResults, res)
				// Ask for the next command (may yield a new dispatch or idle).
				requestNext()

			case "log":
				var msg wsLog
				if err := json.Unmarshal(env.Data, &msg); err != nil {
					log.Printf("decode log: %v", err)
					continue
				}
				if activeCommandID == uuid.Nil {
					continue
				}
				h.bus.Publish(ctx, events.TopicCommandLogs, events.CommandLog{
					CommandID: activeCommandID,
					Line:      msg.Line,
				})

			default:
				log.Printf("agent ws: unknown message type %q", env.Type)
			}

		case cmd := <-dispatchCh:
			// An unsolicited dispatch arrived outside of requestNext. This
			// can happen if the service dispatched before our AgentReady
			// publish consumed it; treat it as a valid command arrival.
			if activeCommandID != uuid.Nil {
				log.Printf("agent ws: dispatch arrived while busy, dropping command %s", cmd.ID)
				continue
			}
			activeCommandID = cmd.ID
			data, err := json.Marshal(cmd)
			if err != nil {
				log.Printf("marshal command: %v", err)
				continue
			}
			if err := conn.WriteJSON(wsEnvelope{Type: "command", Data: data}); err != nil {
				log.Printf("agent ws write command: %v", err)
			}

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

