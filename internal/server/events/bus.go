package events

import "context"

// Handler is a callback invoked when an event is published to a subscribed
// topic. It must not block; use a channel or goroutine for slow work.
type Handler func(v any)

// Bus is a generic publish/subscribe interface. Topics are arbitrary strings.
// Implementations must be safe for concurrent use.
type Bus interface {
	// Subscribe registers handler for topic and returns an unsubscribe func.
	// Calling the returned func removes the handler. It is safe to call from
	// within a handler.
	Subscribe(topic string, handler Handler) (unsubscribe func())
	// Publish delivers v to all current subscribers of topic.
	// The in-memory implementation dispatches synchronously on the caller's
	// goroutine; a broker-backed implementation may deliver asynchronously.
	Publish(ctx context.Context, topic string, v any)
}
