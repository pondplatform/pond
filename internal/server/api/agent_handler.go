package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
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
	CommandID    uuid.UUID `json:"command_id"`
	DeploymentID uuid.UUID `json:"deployment_id"`
}

// wsLog is the payload of a "log" message from the agent.
type wsLog struct {
	Line string `json:"line"`
}

// AgentHandler handles WebSocket connections from agents.
type AgentHandler struct {
	clusters    store.ClusterRepository
	commands    store.CommandRepository
	logs        store.CommandLogRepository
	deployments service.DeploymentService
	bus         events.Bus
}

func NewAgentHandler(
	clusters store.ClusterRepository,
	commands store.CommandRepository,
	logs store.CommandLogRepository,
	deployments service.DeploymentService,
	bus events.Bus,
) *AgentHandler {
	return &AgentHandler{
		clusters:    clusters,
		commands:    commands,
		logs:        logs,
		deployments: deployments,
		bus:         bus,
	}
}

func (h *AgentHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		http.Error(w, "missing authorization", http.StatusUnauthorized)
		return
	}

	hash := sha256hex(token)
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

	// activeCommandID tracks the in-flight command for this connection.
	var (
		cmdMu           sync.Mutex
		activeCommandID uuid.UUID
	)
	setCommandID := func(id uuid.UUID) {
		cmdMu.Lock()
		activeCommandID = id
		cmdMu.Unlock()
	}
	takeCommandID := func() uuid.UUID {
		cmdMu.Lock()
		id := activeCommandID
		activeCommandID = uuid.Nil
		cmdMu.Unlock()
		return id
	}
	getCommandID := func() uuid.UUID {
		cmdMu.Lock()
		id := activeCommandID
		cmdMu.Unlock()
		return id
	}

	// On disconnect, requeue any in-flight command so it can be redelivered.
	defer func() {
		if id := takeCommandID(); id != uuid.Nil {
			if err := h.commands.Requeue(context.Background(), id); err != nil {
				log.Printf("requeue on disconnect: %v", err)
			}
		}
	}()

	// sendCh serialises writes so the reader goroutine and the idle-notify
	// callback (from another goroutine) can both send without racing.
	sendCh := make(chan wsEnvelope, 4)

	// Writer goroutine: drains sendCh and writes to the WebSocket.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case env, ok := <-sendCh:
				if !ok {
					return
				}
				if err := conn.WriteJSON(env); err != nil {
					log.Printf("agent ws write: %v", err)
					return
				}
			}
		}
	}()

	send := func(env wsEnvelope) {
		select {
		case sendCh <- env:
		case <-ctx.Done():
		}
	}

	// idleUnsub holds the current idle-notification unsubscribe func (non-nil
	// only while the agent is registered as idle on the bus).
	var (
		idleMu    sync.Mutex
		idleUnsub func()
	)
	clearIdle := func() {
		idleMu.Lock()
		if idleUnsub != nil {
			idleUnsub()
			idleUnsub = nil
		}
		idleMu.Unlock()
	}

	// sendNextOrIdle dequeues the next command and sends it, or subscribes to
	// the cluster topic and sends an "idle" message.
	var sendNextOrIdle func()
	sendNextOrIdle = func() {
		cmd, err := h.commands.Dequeue(ctx, cluster.ID)
		if err != nil {
			log.Printf("dequeue: %v", err)
			return
		}
		if cmd != nil {
			setCommandID(cmd.ID)
			data, err := json.Marshal(cmd)
			if err != nil {
				log.Printf("marshal command: %v", err)
				return
			}
			send(wsEnvelope{Type: "command", Data: data})
			return
		}

		// No command available — subscribe to the cluster topic so the
		// deployment service can wake us when it enqueues a new command.
		var once sync.Once
		idleMu.Lock()
		idleUnsub = h.bus.Subscribe(events.ClusterTopic(cluster.ID), func(_ any) {
			once.Do(func() {
				clearIdle()
				cmd, err := h.commands.Dequeue(ctx, cluster.ID)
				if err != nil || cmd == nil {
					return
				}
				setCommandID(cmd.ID)
				data, err := json.Marshal(cmd)
				if err != nil {
					log.Printf("marshal command (idle push): %v", err)
					return
				}
				send(wsEnvelope{Type: "command", Data: data})
			})
		})
		idleMu.Unlock()

		send(wsEnvelope{Type: "idle"})
	}

	// Reader loop — the agent drives the protocol.
	for {
		var env wsEnvelope
		if err := conn.ReadJSON(&env); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("agent ws read: %v", err)
			}
			clearIdle()
			return
		}

		switch env.Type {
		case "ready":
			// Agent is ready for its first command (sent on connect).
			sendNextOrIdle()

		case "ack":
			// Agent has begun executing the command — transition deployment to running.
			var ack wsAck
			if err := json.Unmarshal(env.Data, &ack); err != nil {
				log.Printf("decode ack: %v", err)
				continue
			}
			if err := h.deployments.MarkRunning(ctx, ack.DeploymentID); err != nil {
				log.Printf("mark running deployment %s: %v", ack.DeploymentID, err)
				// Non-fatal: deployment stays 'pending' briefly longer.
			}

		case "result":
			// Agent finished executing a command — store the result, publish to
			// the event bus (which triggers the deployment state machine), then
			// dispatch the next command.
			clearIdle()
			var res events.CommandResult
			if err := json.Unmarshal(env.Data, &res); err != nil {
				log.Printf("decode result: %v", err)
				continue
			}
			// Trust the server-tracked command ID, not the agent-supplied one.
			res.CommandID = getCommandID()
			cmdID := takeCommandID()
			if cmdID == uuid.Nil {
				log.Printf("received result with no active command")
				continue
			}
			// Update command status in the database.
			if res.Success {
				if err := h.commands.MarkSucceeded(ctx, cmdID, res.Output); err != nil {
					log.Printf("mark command succeeded %s: %v", cmdID, err)
				}
			} else {
				if err := h.commands.MarkFailed(ctx, cmdID, res.Error); err != nil {
					log.Printf("mark command failed %s: %v", cmdID, err)
				}
			}
			// Publish to the bus — the deployment service's subscriber advances
			// the state machine synchronously (in-memory bus).
			h.bus.Publish(ctx, events.TopicCommandResults, res)
			// Immediately try to dispatch the next command (e.g. helm.upgrade
			// enqueued by processResult above).
			sendNextOrIdle()

		case "log":
			// Agent is streaming a log line from the running command.
			var msg wsLog
			if err := json.Unmarshal(env.Data, &msg); err != nil {
				log.Printf("decode log: %v", err)
				continue
			}
			cmdID := getCommandID()
			if cmdID == uuid.Nil {
				continue
			}
			if err := h.logs.Append(ctx, cmdID, msg.Line); err != nil {
				log.Printf("append log for command %s: %v", cmdID, err)
			}
			h.bus.Publish(ctx, events.TopicCommandLogs, events.CommandLog{
				CommandID: cmdID,
				Line:      msg.Line,
			})

		default:
			log.Printf("agent ws: unknown message type %q", env.Type)
		}
	}
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
