package transferservice

import (
	"context"
	"time"

	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

func (s *Service) runResumeSchedule(ctx context.Context) error {
	for {
		err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `UPDATE transfers SET resume_issued = true WHERE resume_at <= clock_timestamp() AND NOT resume_issued RETURNING transfer_id, trace_context`)
			if err != nil {
				return err
			}
			due, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct {
				ID           string
				TraceContext propagation.MapCarrier
			}])
			if err != nil {
				return err
			}
			for _, transfer := range due {
				resumeContext := otel.GetTextMapPropagator().Extract(context.Background(), transfer.TraceContext)
				command, err := messaging.New(resumeContext, transfer.ID, messaging.ResumeDelivery{TransferID: transfer.ID}, "")
				if err != nil {
					return err
				}
				if err := messaging.Enqueue(resumeContext, tx, messaging.ResumeDeliveryTopic, command); err != nil {
					return err
				}
			}
			return nil
		})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			s.logger.Error("schedule delivery resume", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}
