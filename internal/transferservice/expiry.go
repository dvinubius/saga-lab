package transferservice

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Service) runExpirySweep(ctx context.Context, expiry, interval time.Duration) error {
	for {
		if err := s.expireVisitors(ctx, time.Now().Add(-expiry)); err != nil && ctx.Err() == nil {
			s.logger.Error("expire visitors", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

func (s *Service) expireVisitors(ctx context.Context, unseenSince time.Time) error {
	var errs []error
	closing, err := s.visitorIDs(ctx, `SELECT visitor_id FROM account_closures`)
	if err != nil {
		return err
	}
	for _, id := range closing {
		errs = append(errs, s.closeAccounts(ctx, id))
	}
	expired, err := s.visitorIDs(ctx, `SELECT visitor_id FROM visitors WHERE last_seen_at < $1`, unseenSince)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	forgotten := 0
	for _, id := range expired {
		err := s.forgetVisitor(ctx, id)
		if errors.Is(err, errTransferInProgress) {
			continue
		}
		if err == nil {
			forgotten++
		}
		errs = append(errs, err)
	}
	if forgotten > 0 {
		s.logger.Info("visitors expired", "count", forgotten)
	}
	return errors.Join(errs...)
}

func (s *Service) visitorIDs(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
