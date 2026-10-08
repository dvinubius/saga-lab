package transferservice

import (
	"context"
	"errors"
	"time"

	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

func lockSlot(ctx context.Context, tx pgx.Tx) (string, error) {
	var holder string
	err := tx.QueryRow(ctx, `SELECT COALESCE(holder_transfer_id, '') FROM demonstration_slot FOR UPDATE`).Scan(&holder)
	return holder, err
}

func lockSlotFor(ctx context.Context, tx pgx.Tx, transferID string) error {
	_, err := tx.Exec(ctx, `SELECT FROM demonstration_slot WHERE EXISTS (SELECT 1 FROM transfers WHERE transfer_id = $1 AND scenario = $2) FOR UPDATE`, transferID, bankBUnavailable)
	return err
}

func (s *Service) releaseSlot(ctx context.Context, tx pgx.Tx, transferID string) error {
	var releasable bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
  SELECT 1 FROM demonstration_slot slot JOIN transfers t ON t.transfer_id = slot.holder_transfer_id
  WHERE t.transfer_id = $1 AND (t.status = $2 OR (t.status = $3 AND EXISTS (
   SELECT 1 FROM transfer_history WHERE transfer_id = t.transfer_id AND observation = $4
  )))
 )`, transferID, rejected, completed, deliveryPaused).Scan(&releasable); err != nil {
		return err
	}
	if !releasable {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE demonstration_slot SET holder_transfer_id = NULL`); err != nil {
		return err
	}
	var debit messaging.DebitFunds
	var traceContext propagation.MapCarrier
	var requestedAt time.Time
	err := tx.QueryRow(ctx, `SELECT transfer_id, visitor_id, amount, scenario, trace_context, requested_at FROM transfers
  WHERE status = $1 ORDER BY requested_at, transfer_id LIMIT 1 FOR UPDATE`, awaitingAdmission).
		Scan(&debit.TransferID, &debit.VisitorID, &debit.Amount, &debit.Scenario, &traceContext, &requestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		s.logger.Info("demonstration slot released", "transfer_id", transferID)
		return nil
	}
	if err != nil {
		return err
	}
	admissionContext := otel.GetTextMapPropagator().Extract(context.Background(), traceContext)
	command, err := messaging.New(admissionContext, debit.TransferID, debit, "")
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE transfers SET status = $2 WHERE transfer_id = $1`, debit.TransferID, debitPending); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE demonstration_slot SET holder_transfer_id = $1`, debit.TransferID); err != nil {
		return err
	}
	admittedAt := time.Now()
	if err := record(ctx, tx, debit.TransferID, historyEntry{
		Observation: admitted, Service: messaging.TransferService, ObservedAt: admittedAt, IssuedMessageID: command.UUID,
	}); err != nil {
		return err
	}
	if err := messaging.Enqueue(admissionContext, tx, messaging.DebitFundsTopic, command); err != nil {
		return err
	}
	recordWait(admissionContext, admissionWaitSpan, debit.TransferID, requestedAt, admittedAt)
	s.logger.Info("demonstration slot released", "transfer_id", transferID)
	s.logger.Info("transfer admitted", "transfer_id", debit.TransferID)
	return nil
}
