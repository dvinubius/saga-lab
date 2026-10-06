package transferservice

import (
	"context"
	"slices"
	"time"

	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/jackc/pgx/v5"
)

type activity struct {
	At       time.Time
	Transfer *transferSummary
	TopUp    int64
}

func (s *Service) activity(ctx context.Context, transfers []transferSummary) ([]activity, error) {
	rows, err := s.db.Query(ctx,
		`SELECT amount, topped_up_at FROM top_ups WHERE visitor_id = $1`,
		visitor.ID(ctx),
	)
	if err != nil {
		return nil, err
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (activity, error) {
		var a activity
		err := row.Scan(&a.TopUp, &a.At)
		return a, err
	})
	if err != nil {
		return nil, err
	}
	for i := range transfers {
		entries = append(entries, activity{At: transfers[i].RequestedAt, Transfer: &transfers[i]})
	}
	slices.SortStableFunc(entries, func(a, b activity) int { return b.At.Compare(a.At) })
	return entries, nil
}
