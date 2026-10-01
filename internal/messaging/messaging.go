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
)

const (
	DebitFundsTopic    = "DebitFunds"
	FundsDebitedTopic  = "FundsDebited"
	CreditFundsTopic   = "CreditFunds"
	FundsCreditedTopic = "FundsCredited"
)

const causationIDKey = "causation_id"

type DebitFunds struct {
	TransferID string `json:"transfer_id"`
	VisitorID  string `json:"visitor_id"`
	Amount     int64  `json:"amount"`
}

type FundsDebited struct {
	TransferID string    `json:"transfer_id"`
	ObservedAt time.Time `json:"observed_at"`
}

type CreditFunds struct {
	TransferID string `json:"transfer_id"`
	VisitorID  string `json:"visitor_id"`
	Amount     int64  `json:"amount"`
}

type FundsCredited struct {
	TransferID string    `json:"transfer_id"`
	ObservedAt time.Time `json:"observed_at"`
}

func New(payload any, causationID string) (*message.Message, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %T: %w", payload, err)
	}
	msg := message.NewMessage(watermill.NewUUID(), body)
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
	Publisher  *amqp.Publisher
	Subscriber *amqp.Subscriber
	Router     *message.Router
}

func Connect(url string, publishedTopics ...string) (*Broker, error) {
	if url == "" {
		return nil, errors.New("AMQP URL is not configured")
	}
	config := amqp.NewDurableQueueConfig(url)
	config.Publish.ConfirmDelivery = true
	logger := watermill.NewSlogLogger(slog.Default())

	publisher, err := amqp.NewPublisher(config, logger)
	if err != nil {
		return nil, fmt.Errorf("connect publisher: %w", err)
	}
	subscriber, err := amqp.NewSubscriber(config, logger)
	if err != nil {
		publisher.Close()
		return nil, fmt.Errorf("connect subscriber: %w", err)
	}
	b := &Broker{Publisher: publisher, Subscriber: subscriber}
	for _, topic := range publishedTopics {
		if err := subscriber.SubscribeInitialize(topic); err != nil {
			b.Close()
			return nil, fmt.Errorf("declare %s queue: %w", topic, err)
		}
	}
	b.Router, err = message.NewRouter(message.RouterConfig{}, logger)
	if err != nil {
		b.Close()
		return nil, fmt.Errorf("create router: %w", err)
	}
	return b, nil
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
	if !b.Publisher.IsConnected() {
		return errors.New("publisher is not connected")
	}
	return nil
}

func (b *Broker) Close() error {
	return errors.Join(b.Subscriber.Close(), b.Publisher.Close())
}
