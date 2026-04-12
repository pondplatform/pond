package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
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

// AgentHandler handles WebSocket connections from agents.
type AgentHandler struct {
	clusters store.ClusterRepository
	queue    service.CommandQueue
}

func NewAgentHandler(clusters store.ClusterRepository, queue service.CommandQueue) *AgentHandler {
	return &AgentHandler{clusters: clusters, queue: queue}
}

func (h *AgentHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	// Authenticate via Bearer token.
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

	// resultCh receives the result of the currently executing command.
	resultCh := make(chan *service.CommandResult, 1)

	// Reader goroutine: receives results and logs from the agent.
	go func() {
		for {
			var env wsEnvelope
			if err := conn.ReadJSON(&env); err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("agent ws read: %v", err)
				}
				close(resultCh)
				return
			}

			switch env.Type {
			case "result":
				var res service.CommandResult
				if err := json.Unmarshal(env.Data, &res); err != nil {
					log.Printf("decode result: %v", err)
					continue
				}
				if err := h.queue.Acknowledge(r.Context(), res.CommandID, &res); err != nil {
					log.Printf("acknowledge command: %v", err)
				}
				// Non-blocking send in case the writer goroutine already moved on.
				select {
				case resultCh <- &res:
				default:
				}

			case "log":
				// Log entries are informational; just print them for now.
				log.Printf("agent log: %s", string(env.Data))
			}
		}
	}()

	// Writer goroutine (this goroutine): poll for commands and send them.
	ctx := r.Context()
	for {
		cmd, err := h.queue.Dequeue(ctx, cluster.ID)
		if err != nil {
			log.Printf("dequeue command: %v", err)
			return
		}

		if cmd == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}

		data, err := json.Marshal(cmd)
		if err != nil {
			log.Printf("marshal command: %v", err)
			continue
		}

		env := wsEnvelope{Type: "command", Data: data}
		if err := conn.WriteJSON(env); err != nil {
			log.Printf("agent ws write: %v", err)
			return
		}

		// Wait for result before sending next command.
		select {
		case <-ctx.Done():
			return
		case _, ok := <-resultCh:
			if !ok {
				return // reader closed
			}
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
