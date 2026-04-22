package events

import "context"

// Handler is a callback invoked when an event is published to a subscribed
// topic. It must not block; use a channel or goroutine for slow work.
type Handler func(v any)

// Bus is a generic publish/subscribe interface. Topics are arbitrary strings.
// Implementations must be safe for concurrent use.
type Bus interface {
	// SubscribeWork registers a single-consumer durable handler for topic.
	// Each published message is delivered to exactly one subscriber (round-robin
	// when multiple handlers are registered). Use for state-machine events that
	// must not be dropped or duplicated.
	SubscribeWork(topic string, h Handler) (func(), error)

	// SubscribeFanout registers an ephemeral broadcast handler for topic.
	// Every current subscriber receives each message; no durability guarantee.
	// Use for log streaming and other fire-and-forget fan-out events.
	SubscribeFanout(topic string, h Handler) (func(), error)

	// Publish delivers v to subscribers of topic according to their subscription
	// type: all fanout subscribers receive it, exactly one work subscriber receives it.
	Publish(ctx context.Context, topic string, v any)
}
