package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/agent/wire"
)

type Config struct {
	ServerAddr string
	AgentToken string
	// PollInterval is how long the agent waits before asking for work again
	// after the server had none.
	PollInterval time.Duration
}

// How long the agent waits before connecting again: the first delay, doubled
// after each failed attempt up to the last.
const (
	firstReconnectDelay = time.Second
	lastReconnectDelay  = 30 * time.Second
)

// Run serves the server until ctx is done. Losing the connection doesn't stop
// the agent: it keeps running its command and connects again.
func Run(ctx context.Context, cfg Config, exec CommandExecutor, log *slog.Logger) {
	log.Info("connecting to server", "addr", cfg.ServerAddr)
	newWorker(NewConnection(cfg.ServerAddr, cfg.AgentToken), exec, cfg.PollInterval, log).
		run(ctx, firstReconnectDelay, lastReconnectDelay)
}

// worker holds what outlives a connection: the command the agent runs, the
// results the server has not confirmed, and the log lines it could not send
// yet. The server confirms what it got by answering the next ready.
type worker struct {
	conn AgentConnection
	exec CommandExecutor
	poll time.Duration
	log  *slog.Logger
	logs *logRelay

	running *runningCommand
	unacked []*wire.ResultPayload
	// restarted stays true until the server first answers this agent: until
	// then it may not know that the agent holds nothing from before.
	restarted bool
}

func newWorker(conn AgentConnection, exec CommandExecutor, poll time.Duration, log *slog.Logger) *worker {
	return &worker{
		conn:      conn,
		exec:      exec,
		poll:      poll,
		log:       log,
		logs:      &logRelay{conn: conn},
		restarted: true,
	}
}

// run connects and serves until ctx is done, waiting longer after each failed
// attempt. When it returns, it cancels the running command.
func (w *worker) run(ctx context.Context, firstDelay, lastDelay time.Duration) {
	defer func() {
		if w.running != nil {
			w.running.cancel(nil)
		}
	}()

	delay := firstDelay
	for {
		if err := w.conn.Connect(ctx); err != nil {
			w.log.Warn("could not connect to server", "err", err, "retry_in", delay)
		} else {
			w.log.Info("connected")
			delay = firstDelay
			err := w.serve(ctx)
			if ctx.Err() != nil {
				return
			}
			w.log.Warn("lost connection to server", "err", err, "retry_in", delay)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(2*delay, lastDelay)
	}
}

// serve answers the server on one connection. It opens with a hello, then
// pulls commands one at a time: it asks for work, runs what it gets, reports
// the result, and asks again. When the server has nothing, it waits a poll
// interval before asking again. While a command runs, it keeps reading, so
// the server can tell it to cancel. serve closes the connection when it
// returns.
func (w *worker) serve(ctx context.Context) error {
	inbox := make(chan *wire.Envelope)
	readErr := make(chan error, 1)
	done := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			msg, err := w.conn.ReceiveMessage(ctx)
			if err != nil {
				readErr <- err
				return
			}
			select {
			case inbox <- msg:
			case <-done:
				return
			}
		}
	}()
	// The reader is gone before the next connection opens, so it never reads
	// from that one.
	defer func() {
		close(done)
		w.logs.pause()
		w.conn.Close()
		<-readerDone
	}()

	if err := w.hello(ctx); err != nil {
		return err
	}

	var askAgain <-chan time.Time
	for {
		var finished <-chan *wire.ResultPayload
		if w.running != nil {
			finished = w.running.result
		}

		select {
		case <-ctx.Done():
			return nil

		case err := <-readErr:
			return fmt.Errorf("receive message: %w", err)

		case <-askAgain:
			askAgain = nil
			if err := w.conn.SendReady(ctx); err != nil {
				return fmt.Errorf("send ready: %w", err)
			}

		case result := <-finished:
			w.running = nil
			if err := w.report(ctx, result); err != nil {
				return err
			}

		case msg := <-inbox:
			switch msg.Type {
			case wire.MessageCommand:
				w.confirmed()
				var cmd wire.CommandPayload
				if err := json.Unmarshal(msg.Data, &cmd); err != nil {
					w.log.Error("decode command", "err", err)
					if result := undecodableResult(msg.Data, err); result != nil {
						if err := w.report(ctx, result); err != nil {
							return err
						}
					} else if err := w.conn.SendReady(ctx); err != nil {
						return fmt.Errorf("send ready: %w", err)
					}
					continue
				}
				w.running = start(ctx, w.logs, w.exec, cmd, w.log)

			case wire.MessageIdle:
				w.confirmed()
				askAgain = time.After(w.poll)

			case wire.MessageCancel:
				var cancel wire.CancelPayload
				if err := json.Unmarshal(msg.Data, &cancel); err != nil {
					w.log.Error("decode cancel", "err", err)
					continue
				}
				if w.running != nil && w.running.id == cancel.CommandID {
					w.log.Info("server cancelled the command", "command_id", cancel.CommandID)
					w.running.cancel(errCancelledByServer)
				}

			default:
				w.log.Warn("unexpected message type", "type", msg.Type)
			}
		}
	}
}

// hello opens the connection. It tells the server what the agent holds, then
// sends what the server may have missed: log lines and results. When nothing
// runs, it asks for work.
func (w *worker) hello(ctx context.Context) error {
	if w.running != nil {
		select {
		case result := <-w.running.result:
			// It finished while the agent was disconnected.
			w.running = nil
			w.unacked = append(w.unacked, result)
		default:
		}
	}

	hello := wire.HelloPayload{Restarted: w.restarted}
	if w.running != nil {
		hello.Running = &w.running.id
	}
	for _, result := range w.unacked {
		hello.Finished = append(hello.Finished, result.CommandID)
	}
	if err := w.conn.SendHello(ctx, hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}
	if err := w.logs.resume(ctx); err != nil {
		return fmt.Errorf("send log: %w", err)
	}
	for _, result := range w.unacked {
		if err := w.conn.SendResult(ctx, result); err != nil {
			return fmt.Errorf("send result: %w", err)
		}
	}
	if w.running != nil {
		return nil
	}
	if err := w.conn.SendReady(ctx); err != nil {
		return fmt.Errorf("send ready: %w", err)
	}
	return nil
}

// report sends a result, keeping it until the server confirms it, and asks
// for the next command.
func (w *worker) report(ctx context.Context, result *wire.ResultPayload) error {
	w.unacked = append(w.unacked, result)
	if err := w.conn.SendResult(ctx, result); err != nil {
		return fmt.Errorf("send result: %w", err)
	}
	if err := w.conn.SendReady(ctx); err != nil {
		return fmt.Errorf("send ready: %w", err)
	}
	return nil
}

// confirmed records that the server answered a ready: it has stored every
// result sent before it, and knows what this agent holds.
func (w *worker) confirmed() {
	w.unacked = nil
	w.restarted = false
}

// runningCommand is a command executing in the background.
type runningCommand struct {
	id     uuid.UUID
	cancel context.CancelCauseFunc
	result chan *wire.ResultPayload
}

// start runs cmd in the background; its result arrives on result.
func start(ctx context.Context, logs *logRelay, exec CommandExecutor, cmd wire.CommandPayload, log *slog.Logger) *runningCommand {
	cmdCtx, cancel := context.WithCancelCause(ctx)
	r := &runningCommand{id: cmd.ID, cancel: cancel, result: make(chan *wire.ResultPayload, 1)}
	go func() {
		defer cancel(nil)
		r.result <- execute(cmdCtx, logs, exec, &cmd, log)
	}()
	return r
}

// execute runs one command and returns its result.
func execute(ctx context.Context, logs *logRelay, exec CommandExecutor, cmd *wire.CommandPayload, log *slog.Logger) *wire.ResultPayload {
	log.Info("received command", "type", cmd.Type, "command_id", cmd.ID)

	logSink := func(entry wire.LogPayload) {
		logs.send(ctx, entry)
	}

	result, err := exec.Execute(ctx, cmd, logSink)
	if err != nil {
		log.Error("execute command failed", "command_id", cmd.ID, "err", err)
		return &wire.ResultPayload{
			CommandID:    cmd.ID,
			DeploymentID: cmd.DeploymentID,
			Success:      false,
			Error:        err.Error(),
		}
	}
	result.CommandID = cmd.ID
	log.Info("command completed", "command_id", cmd.ID, "success", result.Success)
	return result
}

// undecodableResult fails the command on the server so the deployment
// records why, instead of the server waiting for a result that never comes.
// Without a readable ID there is nothing to report against, and it returns
// nil.
func undecodableResult(data json.RawMessage, decodeErr error) *wire.ResultPayload {
	var ref struct {
		ID           uuid.UUID `json:"id"`
		DeploymentID uuid.UUID `json:"deployment_id"`
	}
	if err := json.Unmarshal(data, &ref); err != nil || ref.ID == uuid.Nil {
		return nil
	}
	return &wire.ResultPayload{
		CommandID:    ref.ID,
		DeploymentID: ref.DeploymentID,
		Success:      false,
		Error:        fmt.Sprintf("agent could not decode the command (%v); the agent and server versions may differ", decodeErr),
	}
}

// logRelayLimit is how many log lines the agent keeps while it cannot send
// them. Beyond that, it drops the oldest.
const logRelayLimit = 1000

// logRelay sends the running command's log lines. While the agent is
// disconnected it keeps them, and sends them once it is connected again.
type logRelay struct {
	conn AgentConnection

	mu      sync.Mutex
	online  bool
	pending []wire.LogPayload
	dropped int
}

func (r *logRelay) send(ctx context.Context, entry wire.LogPayload) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.online {
		if err := r.conn.SendLog(ctx, entry); err == nil {
			return
		}
		r.online = false
	}
	if len(r.pending) == logRelayLimit {
		r.pending = r.pending[1:]
		r.dropped++
	}
	r.pending = append(r.pending, entry)
}

// resume sends the lines kept while disconnected, in order, after a line
// saying how many were dropped, if any. From then on, lines are sent as they
// come.
func (r *logRelay) resume(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dropped > 0 {
		notice := wire.LogPayload{
			CommandID: r.pending[0].CommandID,
			Line:      fmt.Sprintf("%d log lines were dropped while the agent was disconnected from the server", r.dropped),
			Timestamp: time.Now(),
			Stream:    "stderr",
		}
		if err := r.conn.SendLog(ctx, notice); err != nil {
			return err
		}
		r.dropped = 0
	}
	for len(r.pending) > 0 {
		if err := r.conn.SendLog(ctx, r.pending[0]); err != nil {
			return err
		}
		r.pending = r.pending[1:]
	}
	r.online = true
	return nil
}

// pause keeps lines from now on, until resume.
func (r *logRelay) pause() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.online = false
}
