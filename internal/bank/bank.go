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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
)

//go:embed schema.sql
var schema string

type Role struct {
	commandTopic  string
	outcomeTopics []string
	execute       func(*Bank, *message.Message) error
}

var (
	Source      = Role{messaging.DebitFundsTopic, []string{messaging.FundsDebitedTopic, messaging.DebitRejectedTopic}, (*Bank).debitFunds}
	Destination = Role{messaging.CreditFundsTopic, []string{messaging.FundsCreditedTopic}, (*Bank).creditFunds}
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
	broker, err := messaging.Connect(os.Getenv("AMQP_URL"), config.Role.outcomeTopics...)
	if err != nil {
		return err
	}
	defer broker.Close()
	b.executeCommands(broker)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return broker.Run(ctx) })
	g.Go(func() error { return web.Serve(ctx, ":8080", b.Handler(), web.Internal) })
	return g.Wait()
}

func Open(ctx context.Context, db *pgxpool.Pool, config Config) (*Bank, error) {
	if err := provision(ctx, db, config); err != nil {
		return nil, err
	}
	return &Bank{db: db, role: config.Role}, nil
}

func Reset(ctx context.Context, config Config) error {
	if err := messaging.Purge(os.Getenv("AMQP_URL"), config.Role.commandTopic); err != nil {
		return err
	}
	db, err := postgres.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DROP TABLE IF EXISTS accounts`); err != nil {
			return fmt.Errorf("drop accounts: %w", err)
		}
		return provision(ctx, tx, config)
	})
}

type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func provision(ctx context.Context, db execer, config Config) error {
	if _, err := db.Exec(ctx, schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO accounts (visitor_id, balance) VALUES ($1, $2) ON CONFLICT (visitor_id) DO NOTHING`,
		visitor.PreparedID, config.PreparedBalance,
	); err != nil {
		return fmt.Errorf("provision prepared account: %w", err)
	}
	return nil
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

func (b *Bank) executeCommands(broker *messaging.Broker) {
	b.broker = broker
	broker.Router.AddConsumerHandler(b.role.commandTopic, b.role.commandTopic, broker.Subscriber, func(msg *message.Message) error {
		return b.role.execute(b, msg)
	})
}

func (b *Bank) debitFunds(msg *message.Message) error {
	var command messaging.DebitFunds
	if err := messaging.Decode(msg, &command); err != nil {
		slog.Error("discard command", "error", err)
		return nil
	}
	if command.Amount <= 0 {
		slog.Error("discard debit with non-positive amount", "transfer_id", command.TransferID, "amount", command.Amount)
		return nil
	}
	ctx := msg.Context()
	insufficient := false
	err := pgx.BeginFunc(ctx, b.db, func(tx pgx.Tx) error {
		var balance int64
		if err := tx.QueryRow(ctx,
			`SELECT balance FROM accounts WHERE visitor_id = $1 FOR UPDATE`, command.VisitorID,
		).Scan(&balance); err != nil {
			return err
		}
		if balance < command.Amount {
			insufficient = true
			return nil
		}
		_, err := tx.Exec(ctx,
			`UPDATE accounts SET balance = balance - $2 WHERE visitor_id = $1`,
			command.VisitorID, command.Amount,
		)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("debit not applied: account missing", "transfer_id", command.TransferID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("debit for transfer %s: %w", command.TransferID, err)
	}
	if insufficient {
		return b.publish(command.TransferID, messaging.DebitRejectedTopic, messaging.DebitRejected{
			TransferID: command.TransferID, Reason: "Insufficient funds", ObservedAt: time.Now(),
		}, msg)
	}
	return b.publish(command.TransferID, messaging.FundsDebitedTopic, messaging.FundsDebited{TransferID: command.TransferID, ObservedAt: time.Now()}, msg)
}

func (b *Bank) creditFunds(msg *message.Message) error {
	var command messaging.CreditFunds
	if err := messaging.Decode(msg, &command); err != nil {
		slog.Error("discard command", "error", err)
		return nil
	}
	if command.Amount <= 0 {
		slog.Error("discard credit with non-positive amount", "transfer_id", command.TransferID, "amount", command.Amount)
		return nil
	}
	credited, err := b.db.Exec(msg.Context(),
		`UPDATE accounts SET balance = balance + $2 WHERE visitor_id = $1`,
		command.VisitorID, command.Amount,
	)
	if err != nil {
		return fmt.Errorf("credit for transfer %s: %w", command.TransferID, err)
	}
	if credited.RowsAffected() == 0 {
		slog.Warn("credit not applied: account missing", "transfer_id", command.TransferID)
		return nil
	}
	return b.publish(command.TransferID, messaging.FundsCreditedTopic, messaging.FundsCredited{TransferID: command.TransferID, ObservedAt: time.Now()}, msg)
}

func (b *Bank) publish(transferID, topic string, event any, command *message.Message) error {
	msg, err := messaging.New(command.Context(), transferID, event, command.UUID)
	if err != nil {
		return err
	}
	return b.broker.Publisher.Publish(topic, msg)
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
