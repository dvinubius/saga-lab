package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill-amqp/v3/pkg/amqp"
	"github.com/ThreeDotsLabs/watermill/message"
	amqp091 "github.com/rabbitmq/amqp091-go"
)

const (
	DebitFundsTopic    = "DebitFunds"
	FundsDebitedTopic  = "FundsDebited"
	DebitRejectedTopic = "DebitRejected"
	CreditFundsTopic   = "CreditFunds"
	FundsCreditedTopic = "FundsCredited"
)

const (
	causationIDKey = "causation_id"
	transferIDKey  = "transfer_id"
)

type AccountOperation struct {
	TransferID string `json:"transfer_id"`
	VisitorID  string `json:"visitor_id"`
	Amount     int64  `json:"amount"`
}

type OperationCommitted struct {
	TransferID string    `json:"transfer_id"`
	ObservedAt time.Time `json:"observed_at"`
}

type DebitFunds AccountOperation

type FundsDebited OperationCommitted

type DebitRejected struct {
	TransferID string    `json:"transfer_id"`
	Reason     string    `json:"reason"`
	ObservedAt time.Time `json:"observed_at"`
}

type CreditFunds AccountOperation

type FundsCredited OperationCommitted

func New(ctx context.Context, transferID string, payload any, causationID string) (*message.Message, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %T: %w", payload, err)
	}
	msg := message.NewMessage(watermill.NewUUID(), body)
	msg.SetContext(ctx)
	msg.Metadata.Set(transferIDKey, transferID)
	if causationID != "" {
		msg.Metadata.Set(causationIDKey, causationID)
	}
	return msg, nil
}

func CausationID(msg *message.Message) string {
	return msg.Metadata.Get(causationIDKey)
}

func Decode(msg *message.Message, payload any) error {
	if err := json.Unmarshal(msg.Payload, payload); err != nil {
		return fmt.Errorf("decode %T from message %s: %w", payload, msg.UUID, err)
	}
	return nil
}

type Broker struct {
	Publisher     message.Publisher
	Subscriber    *amqp.Subscriber
	Router        *message.Router
	amqpPublisher *amqp.Publisher
}

func Connect(url string, logger *slog.Logger, publishedTopics ...string) (*Broker, error) {
	config, err := queueConfig(url)
	if err != nil {
		return nil, err
	}
	watermillLogger := watermill.NewSlogLogger(logger)

	publisher, err := amqp.NewPublisher(config, watermillLogger)
	if err != nil {
		return nil, fmt.Errorf("connect publisher: %w", err)
	}
	subscriber, err := amqp.NewSubscriber(config, watermillLogger)
	if err != nil {
		publisher.Close()
		return nil, fmt.Errorf("connect subscriber: %w", err)
	}
	b := &Broker{Publisher: tracingPublisher{publisher}, Subscriber: subscriber, amqpPublisher: publisher}
	for _, topic := range publishedTopics {
		if err := subscriber.SubscribeInitialize(topic); err != nil {
			b.Close()
			return nil, fmt.Errorf("declare %s queue: %w", topic, err)
		}
	}
	b.Router, err = message.NewRouter(message.RouterConfig{}, watermillLogger)
	if err != nil {
		b.Close()
		return nil, fmt.Errorf("create router: %w", err)
	}
	b.Router.AddMiddleware(traceHandling)
	return b, nil
}

func queueConfig(url string) (amqp.Config, error) {
	if url == "" {
		return amqp.Config{}, errors.New("AMQP URL is not configured")
	}
	config := amqp.NewDurableQueueConfig(url)
	config.Publish.ConfirmDelivery = true
	return config, nil
}

func Purge(url string, consumedTopics ...string) error {
	config, err := queueConfig(url)
	if err != nil {
		return err
	}
	conn, err := amqp091.Dial(url)
	if err != nil {
		return fmt.Errorf("connect to broker: %w", err)
	}
	defer conn.Close()
	channel, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	for _, topic := range consumedTopics {
		name := config.Queue.GenerateName(topic)
		queue, err := channel.QueueDeclare(name, config.Queue.Durable, config.Queue.AutoDelete, config.Queue.Exclusive, config.Queue.NoWait, config.Queue.Arguments)
		if err != nil {
			return fmt.Errorf("declare %s queue: %w", name, err)
		}
		if queue.Consumers > 0 {
			return fmt.Errorf("%s queue still has %d consumers; stop the service first", name, queue.Consumers)
		}
		if _, err := channel.QueuePurge(name, false); err != nil {
			return fmt.Errorf("purge %s queue: %w", name, err)
		}
	}
	return nil
}

func (b *Broker) Run(ctx context.Context) error {
	if err := b.Router.Run(ctx); err != nil {
		return err
	}
	if ctx.Err() == nil {
		return errors.New("message router stopped")
	}
	return nil
}

func (b *Broker) Ready() error {
	if !b.Router.IsRunning() || b.Router.IsClosed() {
		return errors.New("message router is not running")
	}
	if !b.amqpPublisher.IsConnected() {
		return errors.New("publisher is not connected")
	}
	return nil
}

func (b *Broker) Close() error {
	return errors.Join(b.Subscriber.Close(), b.amqpPublisher.Close())
}
