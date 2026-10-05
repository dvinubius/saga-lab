package bank

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ThreeDotsLabs/watermill-amqp/v3/pkg/amqp"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	amqp091 "github.com/rabbitmq/amqp091-go"
)

type amqpConsumer struct {
	connection *amqp091.Connection
	channel    *amqp091.Channel
	mu         sync.Mutex
	resumed    map[string]bool
	active     string
	starts     chan resumedStream
}

type resumedStream struct {
	transferID string
	deliveries <-chan amqp091.Delivery
}

const dedicatedConsumerTag = "saga-lab-bank-b-dedicated"

func openDedicated(url string) (*amqpConsumer, error) {
	conn, err := amqp091.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("connect dedicated consumer: %w", err)
	}
	channel, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open dedicated channel: %w", err)
	}
	defer channel.Close()
	if _, err := messaging.DeclareQueue(channel, messaging.CreditFundsDedicatedTopic); err != nil {
		conn.Close()
		return nil, err
	}
	return &amqpConsumer{connection: conn, resumed: map[string]bool{}, starts: make(chan resumedStream, 1)}, nil
}

func (c *amqpConsumer) Resume(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.resumed[id] {
		return nil
	}
	if c.active != "" {
		return errors.New("dedicated consumer is serving another transfer")
	}
	channel, err := c.connection.Channel()
	if err != nil {
		return err
	}
	if err := channel.Qos(1, 0, false); err != nil {
		channel.Close()
		return err
	}
	deliveries, err := channel.Consume(messaging.CreditFundsDedicatedTopic, dedicatedConsumerTag, false, false, false, false, nil)
	if err != nil {
		channel.Close()
		return err
	}
	c.channel = channel
	c.active = id
	c.resumed[id] = true
	c.starts <- resumedStream{transferID: id, deliveries: deliveries}
	return nil
}

func (c *amqpConsumer) Pause(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != id {
		return errors.New("dedicated consumer is not serving this transfer")
	}
	if err := c.channel.Cancel(dedicatedConsumerTag, false); err != nil {
		return err
	}
	if err := c.channel.Close(); err != nil {
		return err
	}
	c.channel = nil
	c.active = ""
	return nil
}

func (c *amqpConsumer) run(ctx context.Context, b *Bank) {
	for {
		select {
		case <-ctx.Done():
			return
		case stream := <-c.starts:
			consume(ctx, b, stream)
		}
	}
}

func consume(ctx context.Context, b *Bank, stream resumedStream) {
	logger := b.logger.With("transfer_id", stream.transferID)
	for {
		select {
		case <-ctx.Done():
			return
		case delivery, ok := <-stream.deliveries:
			if !ok {
				if ctx.Err() == nil {
					logger.Error("dedicated consumer stopped before credit")
				}
				return
			}
			if handleDedicated(ctx, b, delivery, stream.transferID, logger) {
				return
			}
		}
	}
}

func handleDedicated(ctx context.Context, b *Bank, delivery amqp091.Delivery, resumedTransferID string, logger *slog.Logger) bool {
	msg, err := (amqp.DefaultMarshaler{}).Unmarshal(delivery)
	if err != nil {
		logger.Error("discard dedicated delivery", "error", err)
		return delivery.Ack(false) != nil
	}
	if delivery.Redelivered {
		msg.Metadata.Set(amqp.MetadataRedeliveredKey, "true")
	}
	stop := false
	err = messaging.Handle(ctx, messaging.CreditFundsDedicatedTopic, msg, func(msg *message.Message) error {
		var err error
		stop, err = b.dedicatedCredit(msg, resumedTransferID, delivery)
		return err
	})
	if err != nil {
		logger.Error("handle dedicated credit", "error", err)
	}
	return stop
}
