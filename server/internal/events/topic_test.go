package events

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
)

// memBroker delivers published bodies synchronously to every subscriber of a name.
type memBroker struct {
	mu        sync.Mutex
	handlers  map[string][]func([]byte) error
	published map[string]delivery
	subErrs   []error
}

func newMemBroker() *memBroker {
	return &memBroker{handlers: map[string][]func([]byte) error{}, published: map[string]delivery{}}
}

func (m *memBroker) publish(_ context.Context, name string, d delivery, body []byte) error {
	m.mu.Lock()
	m.published[name] = d
	hs := append([]func([]byte) error(nil), m.handlers[name]...)
	m.mu.Unlock()
	for _, h := range hs {
		if err := h(body); err != nil {
			m.mu.Lock()
			m.subErrs = append(m.subErrs, err)
			m.mu.Unlock()
		}
	}
	return nil
}

func (m *memBroker) subscribe(name string, _ delivery, h func([]byte) error) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[name] = append(m.handlers[name], h)
	return func() {}, nil
}

func roundTrip[T any](t *testing.T, pub Publisher[T], sub Subscriber[T], event T) {
	t.Helper()
	var got []T
	if _, err := sub.Subscribe(context.Background(), func(_ context.Context, e T) { got = append(got, e) }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := pub.Publish(context.Background(), event); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(got))
	}
	if !reflect.DeepEqual(got[0], event) {
		t.Errorf("round trip mismatch:\n got  %+v\n want %+v", got[0], event)
	}
}

func TestTopics_RoundTrip(t *testing.T) {
	tp := newTopics(newMemBroker())
	clusterID := uuid.New()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	roundTrip(t, tp.CommandResultsPublisher(), tp.CommandResultsSubscriber(), CommandResult{
		CommandID: uuid.New(), Success: true, Output: json.RawMessage(`{"k":"v"}`), Error: "e",
	})
	roundTrip(t, tp.CommandLogsPublisher(), tp.CommandLogsSubscriber(), CommandLog{CommandID: uuid.New(), Line: "hello"})
	roundTrip(t, tp.AgentReadyPublisher(), tp.AgentReadySubscriber(), AgentReady{ClusterID: clusterID})
	roundTrip(t, tp.CommandStartedPublisher(), tp.CommandStartedSubscriber(), CommandStarted{CommandID: uuid.New()})
	roundTrip(t, tp.AgentDisconnectedPublisher(), tp.AgentDisconnectedSubscriber(), AgentDisconnected{ClusterID: clusterID})
	roundTrip(t, tp.UserInputProvidedPublisher(), tp.UserInputProvidedSubscriber(), UserInputProvided{DeploymentID: uuid.New()})
	roundTrip(t, tp.CommandQueuedPublisher(clusterID), tp.CommandQueuedSubscriber(clusterID), CommandQueued{
		ClusterID: clusterID, CommandID: uuid.New(), DeploymentID: uuid.New(),
	})
	roundTrip(t, tp.CommandDispatchPublisher(clusterID), tp.CommandDispatchSubscriber(clusterID), CommandDispatch{
		Cmd: &domain.Command{
			ID: uuid.New(), ClusterID: clusterID, DeploymentID: uuid.New(), Type: "helm",
			Payload: json.RawMessage(`{"a":1}`), Status: domain.CommandStatusDispatched,
			Output: json.RawMessage(`null`), CreatedAt: now, UpdatedAt: now,
		},
	})
}

func TestTopics_NamesAndDelivery(t *testing.T) {
	b := newMemBroker()
	tp := newTopics(b)
	ctx := context.Background()
	clusterID := uuid.New()

	publishes := []error{
		tp.CommandResultsPublisher().Publish(ctx, CommandResult{}),
		tp.CommandLogsPublisher().Publish(ctx, CommandLog{}),
		tp.AgentReadyPublisher().Publish(ctx, AgentReady{}),
		tp.CommandStartedPublisher().Publish(ctx, CommandStarted{}),
		tp.AgentDisconnectedPublisher().Publish(ctx, AgentDisconnected{}),
		tp.UserInputProvidedPublisher().Publish(ctx, UserInputProvided{}),
		tp.CommandQueuedPublisher(clusterID).Publish(ctx, CommandQueued{}),
		tp.CommandDispatchPublisher(clusterID).Publish(ctx, CommandDispatch{}),
	}
	for i, err := range publishes {
		if err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	want := map[string]delivery{
		"command.results":     work,
		"command.logs":        fanout,
		"agent.ready":         work,
		"command.started":     work,
		"agent.disconnected":  work,
		"user_input.provided": work,
		"cluster." + clusterID.String() + ".command.queued":   work,
		"cluster." + clusterID.String() + ".command.dispatch": work,
	}
	if !reflect.DeepEqual(b.published, want) {
		t.Errorf("published topics mismatch:\n got  %v\n want %v", b.published, want)
	}
}

func TestTopic_SubscribeDecodeError(t *testing.T) {
	b := newMemBroker()
	tp := newTopics(b)

	called := false
	if _, err := tp.AgentReadySubscriber().Subscribe(context.Background(), func(context.Context, AgentReady) { called = true }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := b.publish(context.Background(), "agent.ready", work, []byte("not json")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if called {
		t.Error("handler must not be called for undecodable message")
	}
	if len(b.subErrs) != 1 {
		t.Fatalf("expected 1 decode error reported to broker, got %d", len(b.subErrs))
	}
}
