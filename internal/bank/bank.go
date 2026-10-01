package bank

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/dvinubius/saga-lab/internal/postgres"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/dvinubius/saga-lab/internal/web"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

type Config struct {
	PreparedBalance int64
}

type Bank struct {
	db *pgxpool.Pool
}

func Run(ctx context.Context, config Config) error {
	db, err := postgres.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()
	b, err := Open(ctx, db, config)
	if err != nil {
		return err
	}
	return web.Serve(ctx, ":8080", b.Handler())
}

func Open(ctx context.Context, db *pgxpool.Pool, config Config) (*Bank, error) {
	if _, err := db.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO accounts (visitor_id, balance) VALUES ($1, $2) ON CONFLICT (visitor_id) DO NOTHING`,
		visitor.PreparedID, config.PreparedBalance,
	); err != nil {
		return nil, fmt.Errorf("provision prepared account: %w", err)
	}
	return &Bank{db: db}, nil
}

func (b *Bank) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /accounts/{visitorID}", b.getAccount)
	mux.Handle("GET /readyz", web.Readiness(b.db.Ping))
	return mux
}

type account struct {
	VisitorID string `json:"visitor_id"`
	Balance   int64  `json:"balance"`
}

func (b *Bank) getAccount(w http.ResponseWriter, r *http.Request) {
	a := account{VisitorID: r.PathValue("visitorID")}
	err := b.db.QueryRow(r.Context(),
		`SELECT balance FROM accounts WHERE visitor_id = $1`, a.VisitorID,
	).Scan(&a.Balance)
	if errors.Is(err, pgx.ErrNoRows) {
		web.WriteError(w, http.StatusNotFound, "account not found")
		return
	}
	if err != nil {
		slog.Error("read account", "error", err)
		web.WriteError(w, http.StatusInternalServerError, "account unavailable")
		return
	}
	web.WriteJSON(w, http.StatusOK, a)
}
