package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	domain "github.com/pondplatform/pond/server/internal/model/db"
)

type agentConnectionService struct {
	topics events.Topics
}

func NewAgentConnectionService(topics events.Topics) AgentConnectionService {
	return &agentConnectionService{topics: topics}
}

func (s *agentConnectionService) NewSession(clusterID uuid.UUID, log *slog.Logger) AgentSession {
	return &agentSession{
		clusterID:  clusterID,
		topics:     s.topics,
		log:        log,
		dispatchCh: make(chan *domain.Command, 1),
		wakeCh:     make(chan struct{}, 1),
	}
}

type agentSession struct {
	clusterID  uuid.UUID
	topics     events.Topics
	log        *slog.Logger
	dispatchCh chan *domain.Command
	wakeCh     chan struct{}
	unsubs     []func()
}

func (s *agentSession) Start(ctx context.Context) (<-chan *domain.Command, <-chan struct{}, error) {
	unsubDispatch, err := s.topics.CommandDispatchSubscriber(s.clusterID).Subscribe(ctx, func(_ context.Context, e events.CommandDispatch) {
		select {
		case s.dispatchCh <- e.Cmd:
		default:
			s.log.Warn("dropped duplicate CommandDispatch")
		}
	})
	if err != nil {
		return nil, nil, err
	}
	s.unsubs = append(s.unsubs, unsubDispatch)

	unsubQueued, err := s.topics.CommandQueuedSubscriber(s.clusterID).Subscribe(ctx, func(context.Context, events.CommandQueued) {
		select {
		case s.wakeCh <- struct{}{}:
		default:
		}
	})
	if err != nil {
		for _, unsub := range s.unsubs {
			unsub()
		}
		s.unsubs = nil
		return nil, nil, err
	}
	s.unsubs = append(s.unsubs, unsubQueued)

	return s.dispatchCh, s.wakeCh, nil
}

func (s *agentSession) RequestNext(ctx context.Context) *domain.Command {
	if err := s.topics.AgentReadyPublisher().Publish(ctx, events.AgentReady{ClusterID: s.clusterID}); err != nil {
		s.log.Error("publish agent ready", "err", err)
		return nil
	}
	select {
	case cmd := <-s.dispatchCh:
		return cmd
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

func (s *agentSession) OnAck(ctx context.Context, commandID uuid.UUID) {
	if err := s.topics.CommandStartedPublisher().Publish(ctx, events.CommandStarted{
		CommandID: commandID,
	}); err != nil {
		s.log.Error("publish command started", "command_id", commandID, "err", err)
	}
}

func (s *agentSession) OnResult(ctx context.Context, result events.CommandResult) {
	if err := s.topics.CommandResultsPublisher().Publish(ctx, result); err != nil {
		s.log.Error("publish command result", "command_id", result.CommandID, "err", err)
	}
}

func (s *agentSession) OnLog(ctx context.Context, commandID uuid.UUID, line string) {
	if err := s.topics.CommandLogsPublisher().Publish(ctx, events.CommandLog{
		CommandID: commandID,
		Line:      line,
	}); err != nil {
		s.log.Error("publish command log", "command_id", commandID, "err", err)
	}
}

func (s *agentSession) Close() {
	s.log.Info("agent disconnected")
	if err := s.topics.AgentDisconnectedPublisher().Publish(context.Background(), events.AgentDisconnected{
		ClusterID: s.clusterID,
	}); err != nil {
		s.log.Error("publish agent disconnected", "err", err)
	}
	for _, unsub := range s.unsubs {
		unsub()
	}
	s.unsubs = nil
}
