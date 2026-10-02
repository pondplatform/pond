package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type delivery int

const (
	// work delivers each event to exactly one subscriber via a durable queue.
	work delivery = iota
	// fanout broadcasts each event to every current subscriber; not durable.
	fanout
)

// broker is the byte-level transport a topic is built on.
type broker interface {
	publish(ctx context.Context, name string, d delivery, body []byte) error
	// subscribe calls h for each message on name. An error from h marks the
	// message as undeliverable.
	subscribe(name string, d delivery, h func(body []byte) error) (unsubscribe func(), err error)
}

// topic binds a topic name and delivery mode to a payload type.
type topic[T any] struct {
	name     string
	delivery delivery
	broker   broker
}

func newTopic[T any](b broker, name string, d delivery) *topic[T] {
	return &topic[T]{name: name, delivery: d, broker: b}
}

func (t *topic[T]) Publish(ctx context.Context, event T) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("publish %s: marshal: %w", t.name, err)
	}
	if err := t.broker.publish(ctx, t.name, t.delivery, body); err != nil {
		return fmt.Errorf("publish %s: %w", t.name, err)
	}
	return nil
}

func (t *topic[T]) Subscribe(ctx context.Context, h Handler[T]) (func(), error) {
	unsub, err := t.broker.subscribe(t.name, t.delivery, func(body []byte) error {
		var event T
		if err := json.Unmarshal(body, &event); err != nil {
			return fmt.Errorf("unmarshal %s: %w", t.name, err)
		}
		h(ctx, event)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", t.name, err)
	}
	var once sync.Once
	return func() { once.Do(unsub) }, nil
}
