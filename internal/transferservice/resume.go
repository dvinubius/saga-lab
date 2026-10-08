package transferservice

import (
	"context"
	"time"

	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

type issuedResume struct {
	ID                string
	TraceContext      propagation.MapCarrier
	CreditRequestedAt time.Time
}

func (s *Service) runResumeSchedule(ctx context.Context) error {
	for {
		var waits []wait
		err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `UPDATE transfers t SET resume_issued = true WHERE resume_at <= clock_timestamp() AND NOT resume_issued
  RETURNING transfer_id, trace_context, (SELECT observed_at FROM transfer_history h WHERE h.transfer_id = t.transfer_id AND h.step = $1)`, creditRequested)
			if err != nil {
				return err
			}
			due, err := pgx.CollectRows(rows, pgx.RowToStructByPos[issuedResume])
			if err != nil {
				return err
			}
			issuedAt := time.Now()
			for _, transfer := range due {
				resumeContext := otel.GetTextMapPropagator().Extract(context.Background(), transfer.TraceContext)
				command, err := messaging.New(resumeContext, transfer.ID, messaging.ResumeDelivery{TransferID: transfer.ID}, "")
				if err != nil {
					return err
				}
				if err := messaging.Enqueue(resumeContext, tx, messaging.ResumeDeliveryTopic, command); err != nil {
					return err
				}
				waits = append(waits, wait{resumeContext, deliveryWaitSpan, transfer.ID, transfer.CreditRequestedAt, issuedAt})
			}
			return nil
		})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			s.logger.Error("schedule delivery resume", "error", err)
		} else {
			for _, w := range waits {
				w.record()
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}
