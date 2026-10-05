package transferservice

import (
	"context"
	"time"

	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/jackc/pgx/v5"
)

func (s *Service) runResumeSchedule(ctx context.Context) error {
	for {
		err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `UPDATE transfers SET resume_issued = true WHERE resume_at <= clock_timestamp() AND NOT resume_issued RETURNING transfer_id`)
			if err != nil {
				return err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			for _, id := range ids {
				command, err := messaging.New(ctx, id, messaging.ResumeDelivery{TransferID: id}, "")
				if err != nil {
					return err
				}
				if err := messaging.Enqueue(ctx, tx, messaging.ResumeDeliveryTopic, command); err != nil {
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
