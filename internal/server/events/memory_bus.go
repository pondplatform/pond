package events

import (
	"context"
	"sync"
)

type memoryBus struct {
	mu   sync.RWMutex
	subs map[string]map[uint64]Handler
	seq  uint64
}

// NewMemoryBus returns a synchronous in-memory Bus. Handlers are called on the
// publisher's goroutine in the order they were registered. Safe for concurrent
// Subscribe, Unsubscribe, and Publish calls.
func NewMemoryBus() Bus {
	return &memoryBus{subs: make(map[string]map[uint64]Handler)}
}

func (b *memoryBus) Subscribe(topic string, handler Handler) func() {
	b.mu.Lock()
	b.seq++
	id := b.seq
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[uint64]Handler)
	}
	b.subs[topic][id] = handler
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		delete(b.subs[topic], id)
		b.mu.Unlock()
	}
}

func (b *memoryBus) Publish(_ context.Context, topic string, v any) {
	b.mu.RLock()
	handlers := make([]Handler, 0, len(b.subs[topic]))
	for _, h := range b.subs[topic] {
		handlers = append(handlers, h)
	}
	b.mu.RUnlock()

	for _, h := range handlers {
		h(v)
	}
}
