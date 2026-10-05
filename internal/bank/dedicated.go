package bank

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ThreeDotsLabs/watermill-amqp/v3/pkg/amqp"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	amqp091 "github.com/rabbitmq/amqp091-go"
)

type dedicatedConsumer struct {
	connection *amqp091.Connection
	channel    *amqp091.Channel
	mu         sync.Mutex
	resumed    map[string]bool
	active     string
	starts     chan dedicatedDelivery
}

type dedicatedDelivery struct {
	transferID string
	messages   <-chan amqp091.Delivery
}

const dedicatedConsumerTag = "saga-lab-bank-b-dedicated"

func openDedicated(url string) (*dedicatedConsumer, error) {
	conn, err := amqp091.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("connect dedicated consumer: %w", err)
	}
	return &dedicatedConsumer{connection: conn, resumed: map[string]bool{}, starts: make(chan dedicatedDelivery, 1)}, nil
}

func (d *dedicatedConsumer) Resume(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.resumed[id] {
		return nil
	}
	if d.active != "" {
		return errors.New("dedicated consumer is serving another transfer")
	}
	channel, err := d.connection.Channel()
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
	d.channel = channel
	d.active = id
	d.resumed[id] = true
	d.starts <- dedicatedDelivery{transferID: id, messages: deliveries}
	return nil
}

func (d *dedicatedConsumer) Pause(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active != id {
		return errors.New("dedicated consumer is not serving this transfer")
	}
	if err := d.channel.Cancel(dedicatedConsumerTag, false); err != nil {
		return err
	}
	if err := d.channel.Close(); err != nil {
		return err
	}
	d.channel = nil
	d.active = ""
	return nil
}

func (d *dedicatedConsumer) run(ctx context.Context, b *Bank) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case deliveryStream := <-d.starts:
		consumption:
			for {
				select {
				case <-ctx.Done():
					return nil
				case delivery, ok := <-deliveryStream.messages:
					if !ok {
						if ctx.Err() != nil {
							return nil
						}
						return errors.New("dedicated consumer stopped before credit")
					}
					msg, err := (amqp.DefaultMarshaler{}).Unmarshal(delivery)
					acked, paused := false, false
					if err == nil {
						if delivery.Redelivered {
							msg.Metadata.Set(amqp.MetadataRedeliveredKey, "true")
						}
						err = messaging.Handle(ctx, messaging.CreditFundsDedicatedTopic, msg, func(msg *message.Message) error {
							var err error
							paused, err = b.dedicatedCredit(msg, deliveryStream.transferID, func() error {
								err := delivery.Ack(false)
								acked = err == nil
								return err
							})
							return err
						})
					}
					if err != nil {
						if ctx.Err() != nil {
							return nil
						}
						b.logger.Error("handle dedicated credit", "error", err)
						if acked {
							return err
						}
						if err := delivery.Nack(false, true); err != nil {
							return err
						}
						continue
					}
					if paused {
						break consumption
					}
				}
			}
		}
	}
}
