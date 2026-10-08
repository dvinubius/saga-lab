package transferservice

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const transferRetention = 7 * 24 * time.Hour

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
		s.visitorMu.Lock()
		errs = append(errs, s.closeAccounts(ctx, id))
		s.visitorMu.Unlock()
	}
	expired, err := s.visitorIDs(ctx, `SELECT visitor_id FROM visitors WHERE last_seen_at < $1`, unseenSince)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	forgotten := 0
	for _, id := range expired {
		err := s.expireVisitor(ctx, id, unseenSince)
		if errors.Is(err, errTransferInProgress) || errors.Is(err, errVisitorActive) {
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
	errs = append(errs, s.pruneTransfers(ctx, time.Now().Add(-transferRetention)))
	return errors.Join(errs...)
}

var errVisitorActive = errors.New("visitor is active")

func (s *Service) expireVisitor(ctx context.Context, id string, unseenSince time.Time) error {
	s.visitorMu.Lock()
	defer s.visitorMu.Unlock()
	var expired bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM visitors WHERE visitor_id = $1 AND last_seen_at < $2)`, id, unseenSince).Scan(&expired); err != nil {
		return err
	}
	if !expired {
		return errVisitorActive
	}
	return s.forgetVisitor(ctx, id)
}

func (s *Service) visitorIDs(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (s *Service) pruneTransfers(ctx context.Context, before time.Time) error {
	s.visitorMu.Lock()
	defer s.visitorMu.Unlock()
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		holder, err := lockSlot(ctx, tx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT transfer_id FROM transfers WHERE requested_at < $1 AND NOT (status = ANY($2)) AND transfer_id <> $3 FOR UPDATE`, before, pendingStatuses, holder)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil || len(ids) == 0 {
			return err
		}
		for _, statement := range []string{
			`DELETE FROM transfer_history WHERE transfer_id = ANY($1)`,
			`DELETE FROM transfers WHERE transfer_id = ANY($1)`,
		} {
			if _, err := tx.Exec(ctx, statement, ids); err != nil {
				return err
			}
		}
		return nil
	})
}
