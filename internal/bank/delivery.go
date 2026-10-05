package bank

import (
	"context"
	"errors"
	"fmt"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/jackc/pgx/v5"
)

type DedicatedConsumer interface {
	Resume(transferID string) error
	Pause(transferID string) error
}

type acknowledger interface {
	Ack(multiple bool) error
	Nack(multiple, requeue bool) error
}

func (b *Bank) resumeDelivery(msg *message.Message) error {
	logger := messaging.AttemptLogger(msg.Context(), b.logger)
	var command messaging.ResumeDelivery
	if err := messaging.Decode(msg, &command); err != nil {
		logger.Error("discard command", "error", err)
		return nil
	}
	if command.TransferID == "" {
		logger.Error("discard resume without transfer ID")
		return nil
	}
	resumed := false
	err := b.apply(msg, messaging.ResumeDeliveryTopic, command.TransferID, func(ctx context.Context, tx pgx.Tx) error {
		if b.dedicated == nil {
			return errors.New("dedicated consumer is not configured")
		}
		if err := b.dedicated.Resume(command.TransferID); err != nil {
			return err
		}
		resumed = true
		return b.observe(tx, command.TransferID, messaging.DeliveryResumed, msg)
	}, logger)
	if err != nil {
		logger.Error("resume delivery failed", "transfer_id", command.TransferID, "error", err)
	}
	if err == nil && resumed {
		logger.Info("delivery resumed", "transfer_id", command.TransferID)
	}
	return err
}

func (b *Bank) dedicatedCredit(msg *message.Message, resumedTransferID string, delivery acknowledger) (bool, error) {
	if err := b.creditFunds(msg); err != nil {
		if nackErr := delivery.Nack(false, true); nackErr != nil {
			return true, errors.Join(err, nackErr)
		}
		return false, err
	}
	if err := delivery.Ack(false); err != nil {
		return true, err
	}
	var command messaging.CreditFunds
	if err := messaging.Decode(msg, &command); err != nil || command.TransferID != resumedTransferID || command.Amount <= 0 {
		return false, nil
	}
	if err := b.dedicated.Pause(command.TransferID); err != nil {
		return true, fmt.Errorf("pause delivery: %w", err)
	}
	if err := pgx.BeginFunc(msg.Context(), b.db, func(tx pgx.Tx) error {
		return b.observe(tx, command.TransferID, messaging.DeliveryPaused, msg)
	}); err != nil {
		return true, fmt.Errorf("record delivery paused: %w", err)
	}
	b.logger.Info("delivery paused", "transfer_id", command.TransferID)
	return true, nil
}
