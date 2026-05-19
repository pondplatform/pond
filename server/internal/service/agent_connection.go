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
	bus events.Bus
}

func NewAgentConnectionService(bus events.Bus) AgentConnectionService {
	return &agentConnectionService{bus: bus}
}

func (s *agentConnectionService) NewSession(clusterID uuid.UUID, log *slog.Logger) AgentSession {
	return &agentSession{
		clusterID:  clusterID,
		bus:        s.bus,
		log:        log,
		dispatchCh: make(chan *domain.Command, 1),
		wakeCh:     make(chan struct{}, 1),
	}
}

type agentSession struct {
	clusterID  uuid.UUID
	bus        events.Bus
	log        *slog.Logger
	dispatchCh chan *domain.Command
	wakeCh     chan struct{}
	unsubs     []func()
}

func (s *agentSession) Start(ctx context.Context) (<-chan *domain.Command, <-chan struct{}, error) {
	unsubDispatch, err := s.bus.SubscribeWork(events.ClusterCommandDispatchTopic(s.clusterID), func(v any) {
		e, ok := v.(events.CommandDispatch)
		if !ok {
			return
		}
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

	unsubQueued, err := s.bus.SubscribeWork(events.ClusterCommandQueuedTopic(s.clusterID), func(v any) {
		if _, ok := v.(events.CommandQueued); !ok {
			return
		}
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
	s.bus.Publish(ctx, events.TopicAgentReady, events.AgentReady{ClusterID: s.clusterID})
	select {
	case cmd := <-s.dispatchCh:
		return cmd
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

func (s *agentSession) OnAck(ctx context.Context, deploymentID uuid.UUID, commandId uuid.UUID) {
	s.bus.Publish(ctx, events.TopicCommandStarted, events.CommandStarted{
		DeploymentID: deploymentID,
		CommandID:    commandId,
	})
}

func (s *agentSession) OnResult(ctx context.Context, result events.CommandResult) {
	s.bus.Publish(ctx, events.TopicCommandResults, result)
}

func (s *agentSession) OnLog(ctx context.Context, commandID uuid.UUID, line string) {
	s.bus.Publish(ctx, events.TopicCommandLogs, events.CommandLog{
		CommandID: commandID,
		Line:      line,
	})
}

func (s *agentSession) Close(inFlightCommandID uuid.UUID) {
	s.log.Info("agent disconnected", "in_flight_command_id", inFlightCommandID)
	s.bus.Publish(context.Background(), events.TopicAgentDisconnected, events.AgentDisconnected{
		ClusterID:         s.clusterID,
		InFlightCommandID: inFlightCommandID,
	})
	for _, unsub := range s.unsubs {
		unsub()
	}
	s.unsubs = nil
}
