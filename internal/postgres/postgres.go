package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, errors.New("database URL is not configured")
	}
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("configure database pool: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("reach database: %w", err)
	}
	return db, nil
}
