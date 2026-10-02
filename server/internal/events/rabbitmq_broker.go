package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

type rabbitMQBroker struct {
	conn *amqp.Connection
	log  *slog.Logger

	publishMu sync.Mutex
	pubCh     *amqp.Channel
}

// NewRabbitMQTopics dials RabbitMQ at url and returns the domain topics backed
// by AMQP. The returned closer must be called on shutdown to release the connection.
func NewRabbitMQTopics(url string, log *slog.Logger) (Topics, func(), error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, nil, err
	}
	pubCh, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	b := &rabbitMQBroker{conn: conn, log: log, pubCh: pubCh}
	return newTopics(b), func() { conn.Close() }, nil
}

func (b *rabbitMQBroker) publish(ctx context.Context, name string, mode delivery, body []byte) error {
	msg := amqp.Publishing{ContentType: "application/json", Body: body}

	b.publishMu.Lock()
	defer b.publishMu.Unlock()

	if mode == fanout {
		if err := b.pubCh.ExchangeDeclare(name, "fanout", true, false, false, false, nil); err != nil {
			return fmt.Errorf("rabbitmq: declare exchange: %w", err)
		}
		if err := b.pubCh.PublishWithContext(ctx, name, "", false, false, msg); err != nil {
			return fmt.Errorf("rabbitmq: publish to exchange: %w", err)
		}
		return nil
	}

	if _, err := b.pubCh.QueueDeclare(name, true, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: declare queue: %w", err)
	}
	if err := b.pubCh.PublishWithContext(ctx, "", name, false, false, msg); err != nil {
		return fmt.Errorf("rabbitmq: publish to queue: %w", err)
	}
	return nil
}

func (b *rabbitMQBroker) subscribe(name string, mode delivery, h func([]byte) error) (func(), error) {
	if mode == fanout {
		return b.subscribeFanout(name, h)
	}
	return b.subscribeWork(name, h)
}

// subscribeWork binds a durable work-queue consumer to name. Each message is
// delivered to exactly one consumer and acked after h returns.
func (b *rabbitMQBroker) subscribeWork(name string, h func([]byte) error) (func(), error) {
	ch, err := b.conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: open channel: %w", err)
	}

	if _, err := ch.QueueDeclare(name, true, false, false, false, nil); err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq: declare queue: %w", err)
	}

	tag := "consumer-" + name
	deliveries, err := ch.Consume(name, tag, false, false, false, false, nil)
	if err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq: consume: %w", err)
	}

	go func() {
		for msg := range deliveries {
			if err := h(msg.Body); err != nil {
				b.log.Error("handle message", "topic", name, "err", err)
				msg.Nack(false, false)
				continue
			}
			if err := msg.Ack(false); err != nil {
				b.log.Error("ack message", "topic", name, "err", err)
				return
			}
		}
	}()

	return func() {
		if err := ch.Cancel(tag, false); err != nil {
			b.log.Warn("cancel consumer", "topic", name, "err", err)
		}
		if err := ch.Close(); err != nil {
			b.log.Warn("close channel", "topic", name, "err", err)
		}
	}, nil
}

// subscribeFanout binds an exclusive auto-delete queue to the fanout exchange
// for name. Every current subscriber receives each message.
func (b *rabbitMQBroker) subscribeFanout(name string, h func([]byte) error) (func(), error) {
	ch, err := b.conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: open channel: %w", err)
	}

	if err := ch.ExchangeDeclare(name, "fanout", true, false, false, false, nil); err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq: declare exchange: %w", err)
	}

	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq: declare queue: %w", err)
	}

	if err := ch.QueueBind(q.Name, "", name, false, nil); err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq: bind queue: %w", err)
	}

	deliveries, err := ch.Consume(q.Name, "", true, true, false, false, nil)
	if err != nil {
		ch.Close()
		return nil, fmt.Errorf("rabbitmq: consume: %w", err)
	}

	go func() {
		for msg := range deliveries {
			if err := h(msg.Body); err != nil {
				b.log.Error("handle fanout message", "topic", name, "err", err)
			}
		}
	}()

	return func() { ch.Close() }, nil
}
