package testutil

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
)

// FakeTopic is an in-memory events.Publisher and events.Subscriber. Publish
// records the event and synchronously delivers it to every subscribed handler,
// regardless of the real topic's delivery mode.
type FakeTopic[T any] struct {
	// PublishErr, if set, is returned by Publish; the event is not recorded or delivered.
	PublishErr error

	mu        sync.Mutex
	published []T
	subs      []fakeSub[T]
	nextID    int
}

type fakeSub[T any] struct {
	id  int
	ctx context.Context
	h   events.Handler[T]
}

func (f *FakeTopic[T]) Publish(ctx context.Context, event T) error {
	f.mu.Lock()
	if f.PublishErr != nil {
		f.mu.Unlock()
		return f.PublishErr
	}
	f.published = append(f.published, event)
	subs := append([]fakeSub[T](nil), f.subs...)
	f.mu.Unlock()

	for _, s := range subs {
		s.h(s.ctx, event)
	}
	return nil
}

func (f *FakeTopic[T]) Subscribe(ctx context.Context, h events.Handler[T]) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID
	f.nextID++
	f.subs = append(f.subs, fakeSub[T]{id: id, ctx: ctx, h: h})
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, s := range f.subs {
			if s.id == id {
				f.subs = append(f.subs[:i], f.subs[i+1:]...)
				return
			}
		}
	}, nil
}

// Published returns a copy of every event successfully published so far.
func (f *FakeTopic[T]) Published() []T {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]T(nil), f.published...)
}

// FakeKeyedTopic is a family of FakeTopics keyed by cluster ID, for per-cluster topics.
type FakeKeyedTopic[T any] struct {
	mu     sync.Mutex
	topics map[uuid.UUID]*FakeTopic[T]
}

// For returns the topic for clusterID, creating it on first use.
func (k *FakeKeyedTopic[T]) For(clusterID uuid.UUID) *FakeTopic[T] {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.topics == nil {
		k.topics = map[uuid.UUID]*FakeTopic[T]{}
	}
	t, ok := k.topics[clusterID]
	if !ok {
		t = &FakeTopic[T]{}
		k.topics[clusterID] = t
	}
	return t
}

// PublishedCount returns the number of events published across all clusters.
func (k *FakeKeyedTopic[T]) PublishedCount() int {
	k.mu.Lock()
	topics := make([]*FakeTopic[T], 0, len(k.topics))
	for _, t := range k.topics {
		topics = append(topics, t)
	}
	k.mu.Unlock()

	n := 0
	for _, t := range topics {
		n += len(t.Published())
	}
	return n
}

// MockTopics is an in-memory events.Topics. The zero value is ready to use.
type MockTopics struct {
	CommandResults    FakeTopic[events.CommandResult]
	CommandLogs       FakeTopic[events.CommandLog]
	AgentReady        FakeTopic[events.AgentReady]
	CommandStarted    FakeTopic[events.CommandStarted]
	AgentDisconnected FakeTopic[events.AgentDisconnected]
	UserInputProvided FakeTopic[events.UserInputProvided]
	CommandQueued     FakeKeyedTopic[events.CommandQueued]
	CommandDispatch   FakeKeyedTopic[events.CommandDispatch]
}

var _ events.Topics = (*MockTopics)(nil)

func (m *MockTopics) CommandResultsPublisher() events.Publisher[events.CommandResult] {
	return &m.CommandResults
}
func (m *MockTopics) CommandResultsSubscriber() events.Subscriber[events.CommandResult] {
	return &m.CommandResults
}

func (m *MockTopics) CommandLogsPublisher() events.Publisher[events.CommandLog] {
	return &m.CommandLogs
}
func (m *MockTopics) CommandLogsSubscriber() events.Subscriber[events.CommandLog] {
	return &m.CommandLogs
}

func (m *MockTopics) AgentReadyPublisher() events.Publisher[events.AgentReady] {
	return &m.AgentReady
}
func (m *MockTopics) AgentReadySubscriber() events.Subscriber[events.AgentReady] {
	return &m.AgentReady
}

func (m *MockTopics) CommandStartedPublisher() events.Publisher[events.CommandStarted] {
	return &m.CommandStarted
}
func (m *MockTopics) CommandStartedSubscriber() events.Subscriber[events.CommandStarted] {
	return &m.CommandStarted
}

func (m *MockTopics) AgentDisconnectedPublisher() events.Publisher[events.AgentDisconnected] {
	return &m.AgentDisconnected
}
func (m *MockTopics) AgentDisconnectedSubscriber() events.Subscriber[events.AgentDisconnected] {
	return &m.AgentDisconnected
}

func (m *MockTopics) UserInputProvidedPublisher() events.Publisher[events.UserInputProvided] {
	return &m.UserInputProvided
}
func (m *MockTopics) UserInputProvidedSubscriber() events.Subscriber[events.UserInputProvided] {
	return &m.UserInputProvided
}

func (m *MockTopics) CommandQueuedPublisher(clusterID uuid.UUID) events.Publisher[events.CommandQueued] {
	return m.CommandQueued.For(clusterID)
}
func (m *MockTopics) CommandQueuedSubscriber(clusterID uuid.UUID) events.Subscriber[events.CommandQueued] {
	return m.CommandQueued.For(clusterID)
}

func (m *MockTopics) CommandDispatchPublisher(clusterID uuid.UUID) events.Publisher[events.CommandDispatch] {
	return m.CommandDispatch.For(clusterID)
}
func (m *MockTopics) CommandDispatchSubscriber(clusterID uuid.UUID) events.Subscriber[events.CommandDispatch] {
	return m.CommandDispatch.For(clusterID)
}
