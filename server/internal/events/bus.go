package events

import (
	"context"

	"github.com/google/uuid"
)

// Handler is invoked for every event delivered to a subscription. Handlers run
// synchronously on the subscription's delivery goroutine.
type Handler[T any] func(ctx context.Context, event T)

// Publisher publishes events of type T to a single topic.
type Publisher[T any] interface {
	Publish(ctx context.Context, event T) error
}

// Subscriber subscribes to events of type T on a single topic.
type Subscriber[T any] interface {
	// Subscribe registers h for the topic. ctx is passed to every handler
	// invocation; it does not bound the subscription's lifetime — call the
	// returned unsubscribe func for that.
	Subscribe(ctx context.Context, h Handler[T]) (unsubscribe func(), err error)
}

// Topics is the catalogue of pond's domain topics. Each topic has a fixed
// payload type and delivery mode; see topics for the definitions.
type Topics interface {
	CommandResultsPublisher() Publisher[CommandResult]
	CommandResultsSubscriber() Subscriber[CommandResult]

	CommandLogsPublisher() Publisher[CommandLog]
	CommandLogsSubscriber() Subscriber[CommandLog]

	AgentReadyPublisher() Publisher[AgentReady]
	AgentReadySubscriber() Subscriber[AgentReady]

	CommandStartedPublisher() Publisher[CommandStarted]
	CommandStartedSubscriber() Subscriber[CommandStarted]

	AgentDisconnectedPublisher() Publisher[AgentDisconnected]
	AgentDisconnectedSubscriber() Subscriber[AgentDisconnected]

	CommandQueuedPublisher(clusterID uuid.UUID) Publisher[CommandQueued]
	CommandQueuedSubscriber(clusterID uuid.UUID) Subscriber[CommandQueued]

	CommandDispatchPublisher(clusterID uuid.UUID) Publisher[CommandDispatch]
	CommandDispatchSubscriber(clusterID uuid.UUID) Subscriber[CommandDispatch]
}
