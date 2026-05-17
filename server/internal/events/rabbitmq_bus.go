package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

type rabbitMQBus struct {
	conn *amqp.Connection
	log  *slog.Logger

	publishMu sync.Mutex
	pubCh     *amqp.Channel

	fanoutMu sync.RWMutex
	fanouts  map[string]struct{} // topics declared as fanout exchanges
}

// NewRabbitMQBus dials RabbitMQ at url and returns a Bus backed by AMQP.
// The returned closer must be called on shutdown to release the connection.
func NewRabbitMQBus(url string, log *slog.Logger) (Bus, func(), error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, nil, err
	}
	pubCh, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	b := &rabbitMQBus{
		conn:    conn,
		log:     log,
		pubCh:   pubCh,
		fanouts: make(map[string]struct{}),
	}
	return b, func() { conn.Close() }, nil
}

// SubscribeWork binds a single durable work-queue consumer to topic.
// Each published message is delivered to exactly one subscriber.
// The handler is called synchronously; the message is acked after it returns.
func (b *rabbitMQBus) SubscribeWork(topic string, h Handler) (func(), error) {
	ch, err := b.conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("rabbitmq SubscribeWork %s: open channel: %w", topic, err)
	}

	if _, err := ch.QueueDeclare(topic, true, false, false, false, nil); err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq SubscribeWork %s: declare queue: %w", topic, err)
	}

	tag := "consumer-" + topic
	deliveries, err := ch.Consume(topic, tag, false, false, false, false, nil)
	if err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq SubscribeWork %s: consume: %w", topic, err)
	}

	go func() {
		for d := range deliveries {
			v, err := unmarshalEvent(d.Body)
			if err != nil {
				b.log.Error("unmarshal event", "topic", topic, "err", err)
				d.Nack(false, false)
				continue
			}
			h(v)
			if err := d.Ack(false); err != nil {
				b.log.Error("ack message", "topic", topic, "err", err)
				return
			}
		}
	}()

	return func() {
		if err := ch.Cancel(tag, false); err != nil {
			b.log.Warn("cancel consumer", "topic", topic, "err", err)
		}
		if err := ch.Close(); err != nil {
			b.log.Warn("close channel", "topic", topic, "err", err)
		}
	}, nil
}

// SubscribeFanout binds an ephemeral exclusive queue to a fanout exchange for topic.
// Every current subscriber receives each message; queues are auto-deleted on unsubscribe.
func (b *rabbitMQBus) SubscribeFanout(topic string, h Handler) (func(), error) {
	ch, err := b.conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("rabbitmq SubscribeFanout %s: open channel: %w", topic, err)
	}

	if err := ch.ExchangeDeclare(topic, "fanout", true, false, false, false, nil); err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq SubscribeFanout %s: declare exchange: %w", topic, err)
	}

	b.fanoutMu.Lock()
	b.fanouts[topic] = struct{}{}
	b.fanoutMu.Unlock()

	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq SubscribeFanout %s: declare queue: %w", topic, err)
	}

	if err := ch.QueueBind(q.Name, "", topic, false, nil); err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq SubscribeFanout %s: bind queue: %w", topic, err)
	}

	deliveries, err := ch.Consume(q.Name, "", true, true, false, false, nil)
	if err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq SubscribeFanout %s: consume: %w", topic, err)
	}

	go func() {
		for d := range deliveries {
			v, err := unmarshalEvent(d.Body)
			if err != nil {
				b.log.Error("unmarshal fanout event", "topic", topic, "err", err)
				continue
			}
			h(v)
		}
	}()

	return func() { ch.Close() }, nil
}

// Publish serializes v and delivers it to all subscribers of topic.
func (b *rabbitMQBus) Publish(_ context.Context, topic string, v any) {
	body, err := marshalEvent(v)
	if err != nil {
		b.log.Error("marshal event", "topic", topic, "err", err)
		return
	}

	msg := amqp.Publishing{ContentType: "application/json", Body: body}

	b.fanoutMu.RLock()
	_, isFanout := b.fanouts[topic]
	b.fanoutMu.RUnlock()

	b.publishMu.Lock()
	defer b.publishMu.Unlock()

	if isFanout {
		if err := b.pubCh.ExchangeDeclare(topic, "fanout", true, false, false, false, nil); err != nil {
			b.log.Error("declare fanout exchange", "topic", topic, "err", err)
			return
		}
		if err := b.pubCh.PublishWithContext(context.Background(), topic, "", false, false, msg); err != nil {
			b.log.Error("publish to fanout exchange", "topic", topic, "err", err)
		}
		return
	}

	if _, err := b.pubCh.QueueDeclare(topic, true, false, false, false, nil); err != nil {
		b.log.Error("declare queue", "topic", topic, "err", err)
		return
	}
	if err := b.pubCh.PublishWithContext(context.Background(), "", topic, false, false, msg); err != nil {
		b.log.Error("publish to queue", "topic", topic, "err", err)
	}
}
