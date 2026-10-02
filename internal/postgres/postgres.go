package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, errors.New("database URL is not configured")
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	config.ConnConfig.Tracer = otelpgx.NewTracer(otelpgx.WithDisableAcquireTracer(), otelpgx.WithDisableConnectionDetailsInAttributes())
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("configure database pool: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("reach database: %w", err)
	}
	return db, nil
}
