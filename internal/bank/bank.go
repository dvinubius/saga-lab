package bank

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/dvinubius/saga-lab/internal/web"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
)

//go:embed schema.sql
var schema string

type Role int

const (
	Source Role = iota
	Destination
)

type Config struct {
	PreparedBalance int64
	Role            Role
}

type Bank struct {
	db     *pgxpool.Pool
	role   Role
	broker *messaging.Broker
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
	broker, err := messaging.Connect(os.Getenv("AMQP_URL"), b.outcomeTopic())
	if err != nil {
		return err
	}
	defer broker.Close()
	b.executeCommands(broker)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return broker.Run(ctx) })
	g.Go(func() error { return web.Serve(ctx, ":8080", b.Handler()) })
	return g.Wait()
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
	return &Bank{db: db, role: config.Role}, nil
}

func (b *Bank) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /accounts/{visitorID}", b.getAccount)
	mux.Handle("GET /readyz", web.Readiness(b.ready))
	return mux
}

func (b *Bank) ready(ctx context.Context) error {
	if b.broker != nil {
		if err := b.broker.Ready(); err != nil {
			return err
		}
	}
	return b.db.Ping(ctx)
}

func (b *Bank) outcomeTopic() string {
	if b.role == Source {
		return messaging.FundsDebitedTopic
	}
	return messaging.FundsCreditedTopic
}

func (b *Bank) executeCommands(broker *messaging.Broker) {
	b.broker = broker
	switch b.role {
	case Source:
		broker.Router.AddHandler("debit-funds",
			messaging.DebitFundsTopic, broker.Subscriber,
			messaging.FundsDebitedTopic, broker.Publisher,
			b.debitFunds)
	case Destination:
		broker.Router.AddHandler("credit-funds",
			messaging.CreditFundsTopic, broker.Subscriber,
			messaging.FundsCreditedTopic, broker.Publisher,
			b.creditFunds)
	}
}

func (b *Bank) debitFunds(msg *message.Message) ([]*message.Message, error) {
	var command messaging.DebitFunds
	if err := messaging.Decode(msg, &command); err != nil {
		slog.Error("discard command", "error", err)
		return nil, nil
	}
	if command.Amount <= 0 {
		slog.Error("discard debit with non-positive amount", "transfer_id", command.TransferID, "amount", command.Amount)
		return nil, nil
	}
	debited, err := b.db.Exec(msg.Context(),
		`UPDATE accounts SET balance = balance - $2 WHERE visitor_id = $1 AND balance >= $2`,
		command.VisitorID, command.Amount,
	)
	if err != nil {
		return nil, fmt.Errorf("debit for transfer %s: %w", command.TransferID, err)
	}
	if debited.RowsAffected() == 0 {
		slog.Warn("debit not applied: account missing or funds insufficient", "transfer_id", command.TransferID)
		return nil, nil
	}
	return outcome(messaging.FundsDebited{TransferID: command.TransferID, ObservedAt: time.Now()}, msg)
}

func (b *Bank) creditFunds(msg *message.Message) ([]*message.Message, error) {
	var command messaging.CreditFunds
	if err := messaging.Decode(msg, &command); err != nil {
		slog.Error("discard command", "error", err)
		return nil, nil
	}
	if command.Amount <= 0 {
		slog.Error("discard credit with non-positive amount", "transfer_id", command.TransferID, "amount", command.Amount)
		return nil, nil
	}
	credited, err := b.db.Exec(msg.Context(),
		`UPDATE accounts SET balance = balance + $2 WHERE visitor_id = $1`,
		command.VisitorID, command.Amount,
	)
	if err != nil {
		return nil, fmt.Errorf("credit for transfer %s: %w", command.TransferID, err)
	}
	if credited.RowsAffected() == 0 {
		slog.Warn("credit not applied: account missing", "transfer_id", command.TransferID)
		return nil, nil
	}
	return outcome(messaging.FundsCredited{TransferID: command.TransferID, ObservedAt: time.Now()}, msg)
}

func outcome(event any, command *message.Message) ([]*message.Message, error) {
	msg, err := messaging.New(event, command.UUID)
	if err != nil {
		return nil, err
	}
	return []*message.Message{msg}, nil
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
